package controller

import (
	"github.com/gin-gonic/gin"
	"io"
	"message-pusher/model"
	"sync"
	"time"
)

var messageChanBufferSize = 10
var messageStreamHeartbeatInterval = 15 * time.Second

var messageChanStore struct {
	Map   map[int]map[chan *model.Message]struct{}
	Mutex sync.RWMutex
}

func messageChanStoreAdd(messageChan chan *model.Message, userId int) {
	messageChanStore.Mutex.Lock()
	defer messageChanStore.Mutex.Unlock()
	streams := messageChanStore.Map[userId]
	if streams == nil {
		streams = make(map[chan *model.Message]struct{})
		messageChanStore.Map[userId] = streams
	}
	streams[messageChan] = struct{}{}
}

func messageChanStoreRemove(messageChan chan *model.Message, userId int) {
	messageChanStore.Mutex.Lock()
	defer messageChanStore.Mutex.Unlock()
	streams := messageChanStore.Map[userId]
	delete(streams, messageChan)
	if len(streams) == 0 {
		delete(messageChanStore.Map, userId)
	}
}

func init() {
	messageChanStore.Map = make(map[int]map[chan *model.Message]struct{})
}

func syncMessageToUser(message *model.Message, userId int) {
	messageChanStore.Mutex.RLock()
	defer messageChanStore.Mutex.RUnlock()
	streams := messageChanStore.Map[userId]
	if len(streams) == 0 {
		return
	}
	for messageChan := range streams {
		select {
		case messageChan <- message:
		default:
		} // History remains durable when a slow browser falls behind.
	}
}

func GetNewMessages(c *gin.Context) {
	userId := c.GetInt("id")
	messageChan := make(chan *model.Message, messageChanBufferSize)
	messageChanStoreAdd(messageChan, userId)
	defer func() {
		messageChanStoreRemove(messageChan, userId)
		close(messageChan)
	}()
	heartbeat := time.NewTicker(messageStreamHeartbeatInterval)
	defer heartbeat.Stop()
	if _, err := io.WriteString(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()
	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case msg, ok := <-messageChan:
			if ok {
				c.SSEvent("message", *msg)
				return true
			}
		case <-heartbeat.C:
			_, err := io.WriteString(w, ": keep-alive\n\n")
			return err == nil
		}
		return false
	})
}
