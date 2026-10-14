package controller

// NOT EXECUTED — LOCAL TESTING PROHIBITED.
import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

func TestLegacyAsyncTransactionPersistsOutboxAndRollsBackRejectedSubmission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Message{}); err != nil {
		t.Fatal(err)
	}
	if err := model.MigrateGateway(db); err != nil {
		t.Fatal(err)
	}
	previousDB, previousService := model.DB, gateway.DefaultService()
	model.DB = db
	service := gateway.NewService(db, nil, gateway.SenderFunc(func(context.Context, *model.Notification, *model.Delivery, *model.Target) (gateway.SendResult, error) {
		t.Fatal("submission must not call sender")
		return gateway.SendResult{}, nil
	}), gateway.Options{})
	gateway.SetDefaultService(service)
	t.Cleanup(func() {
		model.DB = previousDB
		gateway.SetDefaultService(previousService)
		sqlDB, e := db.DB()
		if e == nil {
			_ = sqlDB.Close()
		}
	})
	user := model.User{Id: 1, Username: "legacy-test", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, SaveMessageToDatabase: common.SaveMessageToDatabaseAllowed}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	configured := model.Channel{UserId: user.Id, Name: "archive", Type: model.TypeNone, Status: common.ChannelStatusEnabled}
	if err := db.Create(&configured).Error; err != nil {
		t.Fatal(err)
	}
	message := model.Message{Title: "legacy", Content: "durable", Channel: configured.Name, Async: true}
	if err := saveAndSendMessage(context.Background(), &user, &message, &configured); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "gateway_notifications", "gateway_deliveries", "gateway_outbox_events"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s count=%d", table, count)
		}
	}
	if message.Status != common.MessageSendStatusAsyncPending {
		t.Fatal("submission must preserve async pending legacy status")
	}
	if err := db.Model(&model.Source{}).Where("id = ?", common.LegacySourceID(user.Id)).Update("status", model.GatewayStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	rejected := model.Message{Title: "rejected", Content: "must roll back", Channel: configured.Name, Async: true}
	if err := saveAndSendMessage(context.Background(), &user, &rejected, &configured); err == nil {
		t.Fatal("disabled source accepted")
	}
	for _, table := range []string{"messages", "gateway_notifications", "gateway_deliveries", "gateway_outbox_events"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("partial rejected submission persisted in %s", table)
		}
	}
}
