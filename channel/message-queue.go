package channel

import (
	"context"
	"errors"
	"message-pusher/common"
	"message-pusher/model"
	"strconv"
	"sync"
)

var AsyncMessageQueue chan int
var AsyncMessageQueueSize = 128
var AsyncMessageSenderNum = 2

var errGatewayManaged = errors.New("legacy message is managed by the reliable gateway")

func init() { AsyncMessageQueue = make(chan int, AsyncMessageQueueSize) }

func StartLegacyWorkers(ctx context.Context) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	for i := 0; i < AsyncMessageSenderNum; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); asyncMessageSender(ctx) }()
	}
	return wg
}

// LoadAsyncMessages loads async pending messages from database.
// We have to wait the database connection is ready.
func LoadAsyncMessages() {
	LoadAsyncMessagesContext(context.Background())
}

func LoadAsyncMessagesContext(ctx context.Context) {
	ids, err := model.GetAsyncPendingMessageIds()
	if err != nil {
		common.FatalLog("failed to load async pending messages: " + err.Error())
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		case AsyncMessageQueue <- id:
		}
	}
}

func asyncMessageSenderHelper(message *model.Message) error {
	var managed int64
	if err := model.DB.Model(&model.Notification{}).Where("source_id = ? AND idempotency_key = ?", common.LegacySourceID(message.UserId), "legacy:"+strconv.Itoa(message.Id)).Count(&managed).Error; err != nil {
		return err
	}
	if managed > 0 {
		return errGatewayManaged
	}
	user, err := model.GetUserById(message.UserId, false)
	if err != nil {
		return err
	}
	channel_, err := model.GetChannelByName(message.Channel, user.Id)
	if err != nil {
		return err
	}
	return SendMessage(message, user, channel_)
}

func asyncMessageSender(ctx context.Context) {
	for {
		var id int
		select {
		case <-ctx.Done():
			return
		case id = <-AsyncMessageQueue:
		}
		message, err := model.GetMessageById(id)
		if err != nil {
			common.SysError("async message sender error: " + err.Error())
			continue
		}
		err = asyncMessageSenderHelper(message)
		if errors.Is(err, errGatewayManaged) {
			continue
		}
		status := common.MessageSendStatusFailed
		if err != nil {
			common.SysError("async message sender error: " + err.Error())
		} else {
			status = common.MessageSendStatusSent
		}
		err = message.UpdateStatus(status)
		if err != nil {
			common.SysError("async message sender error: " + err.Error())
		}
	}
}
