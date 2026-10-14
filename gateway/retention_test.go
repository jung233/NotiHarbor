package gateway

// NOT EXECUTED — LOCAL TESTING PROHIBITED.
import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"message-pusher/model"
)

func TestRetentionPreservesIdempotencyLatestAndUnknownEvidence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retention.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := model.MigrateGateway(db); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -400)
	notification := model.Notification{Id: "notification", SourceID: "source", IdempotencyKey: "never-reuse", RequestHash: "hash", CreatedAt: old}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{model.DeliveryAccepted, model.DeliveryUnknown, model.DeliveryDeadLetter, model.DeliveryProcessing} {
		delivery := model.Delivery{Id: state, NotificationID: notification.Id, Status: state, CreatedAt: old}
		if err := db.Create(&delivery).Error; err != nil {
			t.Fatal(err)
		}
		for number := 1; number <= 2; number++ {
			attempt := model.DeliveryAttempt{Id: newID(), DeliveryID: state, AttemptNumber: number, Result: state, FinishedAt: &old, CreatedAt: old}
			if err := db.Create(&attempt).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Create(&model.AuditLog{Id: "expired-audit", ActorType: "user", Action: "test", ResourceType: "notification", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{db: db}
	if err := service.PruneLogsOnce(context.Background(), 90, 365); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{model.DeliveryAccepted, model.DeliveryUnknown, model.DeliveryDeadLetter, model.DeliveryProcessing} {
		var count int64
		if err := db.Model(&model.DeliveryAttempt{}).Where("delivery_id = ?", state).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := int64(2)
		if state == model.DeliveryAccepted {
			want = 1
		}
		if count != want {
			t.Fatalf("%s evidence: got %d want %d", state, count, want)
		}
	}
	var latest model.DeliveryAttempt
	if err := db.Where("delivery_id = ?", model.DeliveryAccepted).First(&latest).Error; err != nil || latest.AttemptNumber != 2 {
		t.Fatal("latest attempt must remain", err)
	}
	var count int64
	db.Model(&model.Notification{}).Where("idempotency_key = ?", "never-reuse").Count(&count)
	if count != 1 {
		t.Fatal("retention removed idempotency record")
	}
	db.Model(&model.AuditLog{}).Count(&count)
	if count != 0 {
		t.Fatal("expired audit entry remained")
	}
}
