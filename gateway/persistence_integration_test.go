package gateway

// NOT EXECUTED — LOCAL TESTING PROHIBITED. Redis/MySQL cases require CI fixtures.
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"message-pusher/common"
	"message-pusher/model"
)

func persistenceFixture(t *testing.T, mysqlDSN string) (*gorm.DB, EnqueueRequest) {
	t.Helper()
	var dialector gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "queue.db"))
	if mysqlDSN != "" {
		dialector = mysql.Open(mysqlDSN)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatal(err)
	}
	if err := model.MigrateGateway(db); err != nil {
		t.Fatal(err)
	}
	configured := model.Channel{Type: model.TypeCustom, UserId: 1, Name: newID()[:20], Status: common.ChannelStatusEnabled}
	if err := db.Create(&configured).Error; err != nil {
		t.Fatal(err)
	}
	id := strconv.Itoa(configured.Id)
	source := model.Source{Id: newID(), OwnerUserID: 1, Name: "CI fixture", Status: model.GatewayStatusActive, AllowedChannelIDsJSON: "[\"" + id + "\"]"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	req := EnqueueRequest{SourceID: source.Id, IdempotencyKey: newID(), Title: "fixture", Content: "body", Deliveries: []DeliverySpec{{ChannelID: id}}}
	t.Cleanup(func() {
		var ids []string
		db.Model(&model.Notification{}).Where("source_id = ?", source.Id).Pluck("id", &ids)
		var deliveryIDs []string
		if len(ids) > 0 {
			db.Model(&model.Delivery{}).Where("notification_id IN ?", ids).Pluck("id", &deliveryIDs)
			if len(deliveryIDs) > 0 {
				db.Where("delivery_id IN ?", deliveryIDs).Delete(&model.DeliveryAttempt{})
				db.Where("delivery_id IN ?", deliveryIDs).Delete(&model.OutboxEvent{})
			}
			db.Where("notification_id IN ?", ids).Delete(&model.Delivery{})
			db.Where("source_id = ?", source.Id).Delete(&model.Notification{})
		}
		db.Where("id = ?", source.Id).Delete(&model.Source{})
		db.Where("id = ?", configured.Id).Delete(&model.Channel{})
	})
	return db, req
}

func TestNotificationOutboxAtomicityAndIdempotency(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { checkPersistence(t, "") })
	t.Run("mysql-ci", func(t *testing.T) {
		dsn := os.Getenv("CI_MYSQL_DSN")
		if dsn == "" {
			t.Skip("CI MySQL fixture not provided")
		}
		checkPersistence(t, dsn)
	})
}

func TestSessionHistoryUnionSQLCI(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { checkSessionHistoryUnionSQL(t, "") })
	t.Run("mysql-ci", func(t *testing.T) {
		dsn := os.Getenv("CI_MYSQL_DSN")
		if dsn == "" {
			t.Skip("CI MySQL fixture not provided")
		}
		checkSessionHistoryUnionSQL(t, dsn)
	})
}

func checkSessionHistoryUnionSQL(t *testing.T, dsn string) {
	t.Helper()
	db, req := persistenceFixture(t, dsn)
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatal(err)
	}
	legacySourceID := common.LegacySourceID(1)
	legacySource := model.Source{
		Id: legacySourceID, OwnerUserID: 1, Name: "Legacy /push",
		AllowedChannelIDsJSON: "[\"" + req.Deliveries[0].ChannelID + "\"]", Status: model.GatewayStatusActive,
	}
	if err := db.Create(&legacySource).Error; err != nil {
		t.Fatal(err)
	}
	unbridged := model.Message{
		Link: newID(), UserId: 1, Title: "unbridged legacy", Content: "archive", Channel: "fixture",
		Timestamp: time.Now().Add(-time.Hour).Unix(), Status: common.MessageSendStatusSent,
	}
	bridged := model.Message{
		Link: newID(), UserId: 1, Title: "bridged legacy", Content: "already represented", Channel: "fixture",
		Timestamp: time.Now().Unix(), Status: common.MessageSendStatusAsyncPending,
	}
	for _, message := range []*model.Message{&unbridged, &bridged} {
		if err := db.Create(message).Error; err != nil {
			t.Fatal(err)
		}
	}
	createdAt := time.Now().UTC()
	bridgedNotification := model.Notification{
		Id: newID(), SourceID: legacySourceID, IdempotencyKey: "legacy:" + strconv.Itoa(bridged.Id),
		RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Title:       bridged.Title, Content: bridged.Content,
		MetadataJSON: "{\"legacy_message_id\":" + strconv.Itoa(bridged.Id) + "}",
		Priority:     "normal", DeliveryPolicy: PolicyBroadcast, Status: model.NotificationQueued,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := db.Create(&bridgedNotification).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("id = ?", bridgedNotification.Id).Delete(&model.Notification{}).Error
		_ = db.Where("id = ?", legacySourceID).Delete(&model.Source{}).Error
		_ = db.Where("id IN ?", []int{unbridged.Id, bridged.Id}).Delete(&model.Message{}).Error
	})

	service := NewService(db, nil, nil, Options{})
	from, to := createdAt.Add(-2*time.Hour), createdAt.Add(time.Second)
	filter := NotificationFilter{
		From: &from, To: &to, OwnerUserID: 1, Search: "legacy",
		Page: 1, PageSize: 1, IncludeLegacyArchive: true,
	}
	pageOne, total, err := service.ListNotifications(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(pageOne) != 1 || pageOne[0].Id != bridgedNotification.Id {
		t.Fatalf("first union page=%+v total=%d; want bridged gateway row then one archive row", pageOne, total)
	}
	filter.Page = 2
	pageTwo, secondTotal, err := service.ListNotifications(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if secondTotal != 2 || len(pageTwo) != 1 || pageTwo[0].Id != LegacyArchiveIDPrefix+strconv.Itoa(unbridged.Id) {
		t.Fatalf("second union page=%+v total=%d; want the unbridged archive row", pageTwo, secondTotal)
	}
}

func checkPersistence(t *testing.T, dsn string) {
	db, req := persistenceFixture(t, dsn)
	s := NewService(db, nil, nil, Options{})
	n, duplicate, err := s.Enqueue(context.Background(), req)
	if err != nil || duplicate {
		t.Fatalf("first enqueue: %v replay=%v", err, duplicate)
	}
	replayed, duplicate, err := s.Enqueue(context.Background(), req)
	if err != nil || !duplicate || replayed.Id != n.Id {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	changed := req
	changed.Content = "different"
	if _, _, err := s.Enqueue(context.Background(), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting key: %v", err)
	}
	bad := req
	bad.IdempotencyKey = newID()
	bad.Deliveries = []DeliverySpec{{ChannelID: req.Deliveries[0].ChannelID}, {ChannelID: "unauthorized"}}
	if _, _, err := s.Enqueue(context.Background(), bad); err == nil {
		t.Fatal("unauthorized second route accepted")
	}
	for _, entity := range []interface{}{&model.Notification{}, &model.Delivery{}, &model.OutboxEvent{}} {
		var count int64
		query := db.Model(entity)
		switch entity.(type) {
		case *model.Notification:
			query = query.Where("source_id = ?", req.SourceID)
		case *model.Delivery:
			query = query.Where("notification_id = ?", n.Id)
		case *model.OutboxEvent:
			query = query.Where("delivery_id IN (?)", db.Model(&model.Delivery{}).Select("id").Where("notification_id = ?", n.Id))
		}
		if err := query.Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("atomic/replay rows %T: %d %v", entity, count, err)
		}
	}
}

func TestRedisOutboxDeliveryAndAckCIOnly(t *testing.T) {
	redisURL := os.Getenv("CI_REDIS_URL")
	if redisURL == "" {
		t.Skip("CI Redis fixture not provided")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(options)
	t.Cleanup(func() { _ = rdb.Close() })
	db, req := persistenceFixture(t, "")
	stream := "message-pusher:ci:" + newID()
	t.Cleanup(func() { rdb.Del(context.Background(), stream) })
	calls := 0
	s := NewService(db, rdb, SenderFunc(func(context.Context, *model.Notification, *model.Delivery, *model.Target) (SendResult, error) {
		calls++
		return SendResult{HTTPStatus: 200, ProviderMessageID: "mock-provider-id"}, nil
	}), Options{StreamName: stream, ConsumerGroup: "ci", ConsumerName: "ci", BatchSize: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.ensureConsumerGroup(ctx); err != nil {
		t.Fatal(err)
	}
	n, _, err := s.Enqueue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := s.DispatchOutboxBatch(ctx); err != nil || count != 1 {
		t.Fatalf("outbox publish: %d %v", count, err)
	}
	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "ci", Consumer: "ci", Streams: []string{stream, ">"}, Count: 1, Block: time.Second}).Result()
	if err != nil || len(streams) != 1 || len(streams[0].Messages) != 1 {
		t.Fatalf("stream read: %v", err)
	}
	message := streams[0].Messages[0]
	if err := s.processStreamMessage(ctx, "ci", message); err != nil {
		t.Fatal(err)
	}
	if err := s.processStreamMessage(ctx, "ci", message); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("duplicate stream sent %d times", calls)
	}
	var stored model.Notification
	if err := db.Where("id = ?", n.Id).First(&stored).Error; err != nil || stored.Status != model.NotificationAccepted {
		t.Fatalf("terminal notification: %s %v", stored.Status, err)
	}
	pending, err := rdb.XPending(ctx, stream, "ci").Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("pending entry remained: %v", err)
	}
}
