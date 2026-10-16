package controller

// NOT EXECUTED — LOCAL TESTING PROHIBITED.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"message-pusher/channel"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/middleware"
	"message-pusher/model"
)

type gatewayAPITestSender struct {
	calls int
}

func (s *gatewayAPITestSender) Send(context.Context, *model.Notification, *model.Delivery, *model.Target) (gateway.SendResult, error) {
	s.calls++
	return gateway.SendResult{HTTPStatus: http.StatusOK, ProviderMessageID: "mock-provider-id"}, nil
}

type gatewayAPITestFixture struct {
	db      *gorm.DB
	service *gateway.Service
	sender  *gatewayAPITestSender
	ownerID int
}

func newGatewayAPITestFixture(t *testing.T) *gatewayAPITestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gateway-api.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Channel{}); err != nil {
		t.Fatal(err)
	}
	ownerID := 42
	if err := db.Create(&model.User{
		Id: ownerID, Username: "gateway-api-test-owner",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.MigrateGateway(db); err != nil {
		t.Fatal(err)
	}
	previousDB, previousService := model.DB, gateway.DefaultService()
	model.DB = db
	sender := &gatewayAPITestSender{}
	service := gateway.NewService(db, nil, sender, gateway.Options{})
	gateway.SetDefaultService(service)
	t.Cleanup(func() {
		model.DB = previousDB
		gateway.SetDefaultService(previousService)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return &gatewayAPITestFixture{db: db, service: service, sender: sender, ownerID: ownerID}
}

func (f *gatewayAPITestFixture) createChannel(t *testing.T, name, channelType, accountID string) *model.Channel {
	t.Helper()
	configured := &model.Channel{
		UserId: f.ownerID, Name: name, Type: channelType,
		Status: common.ChannelStatusEnabled, AccountId: accountID,
	}
	if err := f.db.Create(configured).Error; err != nil {
		t.Fatal(err)
	}
	return configured
}

func (f *gatewayAPITestFixture) createSource(t *testing.T, name string, channelIDs ...string) *model.Source {
	t.Helper()
	source, err := f.service.CreateSource(context.Background(), f.ownerID, gateway.SourceInput{
		Name: name, AllowedChannelIDs: channelIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func (f *gatewayAPITestFixture) createKey(t *testing.T, sourceID, name string, scopes ...string) (*model.APIKey, string) {
	t.Helper()
	key, plaintext, err := f.service.CreateAPIKey(context.Background(), f.ownerID, sourceID, name, scopes, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key, plaintext
}

func gatewayTestAPIKeyContext(key *model.APIKey, source *model.Source) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.GatewayAPIKeyContext, key)
		c.Set(middleware.GatewaySourceContext, source)
		c.Set(middleware.GatewaySourceIDContext, source.Id)
		c.Next()
	}
}

func gatewayTestSessionContext(ownerID int) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("id", ownerID)
		c.Set("role", common.RoleCommonUser)
		c.Next()
	}
}

func gatewayAPIRequest(t *testing.T, engine *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func gatewayResponseData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var envelope struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	return envelope.Data
}

func enqueueGatewayTestNotification(t *testing.T, service *gateway.Service, source *model.Source, channel *model.Channel, idempotencyKey string) *model.Notification {
	t.Helper()
	notification, _, err := service.Enqueue(context.Background(), gateway.EnqueueRequest{
		SourceID: source.Id, IdempotencyKey: idempotencyKey, Title: "other source",
		Content: "private notification", Deliveries: []gateway.DeliverySpec{{
			ChannelID: strconv.Itoa(channel.Id), ChannelName: channel.Name,
			ChannelType: channel.Type, Target: channel.AccountId,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return notification
}

func TestGatewayNotificationIdempotencyAndSourceIsolation(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	channel := f.createChannel(t, "api-channel", model.TypeNone, "destination")
	channelID := strconv.Itoa(channel.Id)
	sourceA := f.createSource(t, "source-a", channelID)
	sourceB := f.createSource(t, "source-b", channelID)
	keyA, _ := f.createKey(t, sourceA.Id, "source-a-key", gateway.ScopeNotificationsWrite, gateway.ScopeNotificationsRead)
	engine := gin.New()
	engine.POST("/api/v1/notifications", gatewayTestAPIKeyContext(keyA, sourceA), CreateGatewayNotification)
	engine.GET("/api/v1/notifications", gatewayTestAPIKeyContext(keyA, sourceA), ListGatewayNotifications)
	engine.GET("/api/v1/notifications/:id", gatewayTestAPIKeyContext(keyA, sourceA), GetGatewayNotification)

	body := fmt.Sprintf(`{"title":"Disk alert","content":"space low","channels":[%q],"idempotency_key":"same-request"}`, channelID)
	first := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/notifications", body, nil)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submission status=%d, body=%s", first.Code, first.Body.String())
	}
	firstID, _ := gatewayResponseData(t, first)["notification_id"].(string)
	if firstID == "" {
		t.Fatal("first submission omitted notification_id")
	}
	replay := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/notifications", body, nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("idempotent replay status=%d, body=%s", replay.Code, replay.Body.String())
	}
	if replayID, _ := gatewayResponseData(t, replay)["notification_id"].(string); replayID != firstID {
		t.Fatalf("replay returned notification %q, want %q", replayID, firstID)
	}
	conflictBody := fmt.Sprintf(`{"title":"Disk alert","content":"different body","channels":[%q],"idempotency_key":"same-request"}`, channelID)
	conflict := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/notifications", conflictBody, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d, want 409", conflict.Code)
	}

	missingKeyBody := fmt.Sprintf(`{"title":"No key","content":"still deliver","channels":[%q]}`, channelID)
	withoutKey := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/notifications", missingKeyBody, nil)
	if withoutKey.Code != http.StatusAccepted {
		t.Fatalf("omitted idempotency key status=%d, want 202", withoutKey.Code)
	}
	missingKeyID, _ := gatewayResponseData(t, withoutKey)["notification_id"].(string)
	if missingKeyID == "" || missingKeyID == firstID {
		t.Fatal("omitted idempotency key did not create an independent notification")
	}

	otherSourceNotification := enqueueGatewayTestNotification(t, f.service, sourceB, channel, "source-b-private")
	detail := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/notifications/"+otherSourceNotification.Id, "", nil)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("cross-source detail status=%d, want 404", detail.Code)
	}
	list := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/notifications?source_id="+sourceB.Id, "", nil)
	if list.Code != http.StatusForbidden {
		t.Fatalf("cross-source list status=%d, want 403", list.Code)
	}
}

func TestSessionNotificationHistoryIncludesUnbridgedLegacyArchivesOnly(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	if err := f.db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatal(err)
	}
	channel := f.createChannel(t, "history-channel", model.TypeNone, "destination")
	channelID := strconv.Itoa(channel.Id)
	legacySourceID := common.LegacySourceID(f.ownerID)
	legacySource := model.Source{
		Id: legacySourceID, OwnerUserID: f.ownerID, Name: "Legacy /push",
		AllowedChannelIDsJSON: "[\"" + channelID + "\"]", Status: model.GatewayStatusActive,
	}
	if err := f.db.Create(&legacySource).Error; err != nil {
		t.Fatal(err)
	}
	archiveOld := model.Message{
		UserId: f.ownerID, Title: "archive old", Content: "legacy body", Channel: channel.Name,
		Timestamp: time.Now().Add(-2 * time.Hour).Unix(), Status: common.MessageSendStatusSent, Link: uuid.NewString(),
	}
	archiveNew := model.Message{
		UserId: f.ownerID, Title: "archive new", Content: "another legacy body", Channel: channel.Name,
		Timestamp: time.Now().Add(-time.Hour).Unix(), Status: common.MessageSendStatusSent, Link: uuid.NewString(),
	}
	bridgedMessage := model.Message{
		UserId: f.ownerID, Title: "bridged row", Content: "already in gateway", Channel: channel.Name,
		Timestamp: time.Now().Add(-30 * time.Minute).Unix(), Status: common.MessageSendStatusAsyncPending, Link: uuid.NewString(),
	}
	for _, message := range []*model.Message{&archiveOld, &archiveNew, &bridgedMessage} {
		if err := f.db.Create(message).Error; err != nil {
			t.Fatal(err)
		}
	}
	bridgedNotification, _, err := f.service.Enqueue(context.Background(), gateway.EnqueueRequest{
		SourceID: legacySourceID, IdempotencyKey: "legacy:" + strconv.Itoa(bridgedMessage.Id),
		Title: bridgedMessage.Title, Content: bridgedMessage.Content,
		Deliveries: []gateway.DeliverySpec{{ChannelID: channelID, ChannelName: channel.Name, ChannelType: channel.Type}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collisionSource := f.createSource(t, "history-collision-source", channelID)
	collisionNotification, _, err := f.service.Enqueue(context.Background(), gateway.EnqueueRequest{
		SourceID: collisionSource.Id, IdempotencyKey: "legacy:" + strconv.Itoa(archiveOld.Id),
		Title: "external collision", Content: "must not hide the historical row",
		Deliveries: []gateway.DeliverySpec{{ChannelID: channelID, ChannelName: channel.Name, ChannelType: channel.Type}},
	})
	if err != nil {
		t.Fatal(err)
	}
	normalSource := f.createSource(t, "history-source", channelID)
	normalNotification := enqueueGatewayTestNotification(t, f.service, normalSource, channel, "history-normal")
	key, _ := f.createKey(t, normalSource.Id, "history-key", gateway.ScopeNotificationsRead)
	legacyKey, _ := f.createKey(t, legacySourceID, "legacy-history-key", gateway.ScopeNotificationsRead)

	otherOwnerID := 43
	if err := f.db.Create(&model.User{
		Id: otherOwnerID, Username: "history-private-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error; err != nil {
		t.Fatal(err)
	}
	otherArchive := model.Message{
		UserId: otherOwnerID, Title: "private archive", Content: "other user's content",
		Timestamp: time.Now().Unix(), Status: common.MessageSendStatusSent, Link: uuid.NewString(),
	}
	if err := f.db.Create(&otherArchive).Error; err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	engine.GET("/api/v1/admin/notifications", gatewayTestSessionContext(f.ownerID), ListGatewayNotifications)
	engine.GET("/api/v1/admin/notifications/:id", gatewayTestSessionContext(f.ownerID), GetGatewayNotification)
	engine.POST("/api/v1/admin/notifications/:id/retry", gatewayTestSessionContext(f.ownerID), RetryGatewayNotification)
	engine.POST("/api/v1/admin/notifications/:id/cancel", gatewayTestSessionContext(f.ownerID), CancelGatewayNotification)
	engine.POST("/api/v1/admin/notifications/:id/resend", gatewayTestSessionContext(f.ownerID), ResendGatewayNotification)
	engine.GET("/api/v1/notifications", gatewayTestAPIKeyContext(key, normalSource), ListGatewayNotifications)

	list := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications?page_size=100", "", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("session notification history status=%d, body=%s", list.Code, list.Body.String())
	}
	data := gatewayResponseData(t, list)
	if total, _ := data["total"].(float64); total != 5 {
		t.Fatalf("session history total=%v, want two archive rows and three gateway rows", data["total"])
	}
	items, _ := data["items"].([]interface{})
	found := map[string]int{}
	for _, raw := range items {
		item, _ := raw.(map[string]interface{})
		id, _ := item["id"].(string)
		found[id]++
	}
	for _, id := range []string{
		"legacy-archive:" + strconv.Itoa(archiveOld.Id),
		"legacy-archive:" + strconv.Itoa(archiveNew.Id),
		bridgedNotification.Id, collisionNotification.Id, normalNotification.Id,
	} {
		if found[id] != 1 {
			t.Fatalf("history contains id %q %d times, want exactly once; IDs=%v", id, found[id], found)
		}
	}
	if found["legacy-archive:"+strconv.Itoa(bridgedMessage.Id)] != 0 || found["legacy-archive:"+strconv.Itoa(otherArchive.Id)] != 0 {
		t.Fatalf("bridged or other-user legacy message leaked into history: %v", found)
	}

	legacySourceList := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications?page_size=100&source_id="+legacySourceID, "", nil)
	if legacySourceList.Code != http.StatusOK {
		t.Fatalf("legacy source history status=%d, body=%s", legacySourceList.Code, legacySourceList.Body.String())
	}
	legacySourceData := gatewayResponseData(t, legacySourceList)
	if total, _ := legacySourceData["total"].(float64); total != 3 {
		t.Fatalf("legacy source history total=%v, want two archives and one bridged gateway row", legacySourceData["total"])
	}

	adminEngine := gin.New()
	adminEngine.GET("/api/v1/admin/notifications", func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("role", common.RoleAdminUser)
		c.Next()
	}, ListGatewayNotifications)
	adminList := gatewayAPIRequest(t, adminEngine, http.MethodGet, "/api/v1/admin/notifications?page_size=100", "", nil)
	if adminList.Code != http.StatusOK {
		t.Fatalf("admin notification history status=%d, body=%s", adminList.Code, adminList.Body.String())
	}
	adminData := gatewayResponseData(t, adminList)
	if total, _ := adminData["total"].(float64); total != 6 {
		t.Fatalf("admin history total=%v, want all five gateway/owner rows plus the other user's archive", adminData["total"])
	}
	adminItems, _ := adminData["items"].([]interface{})
	adminFoundOtherArchive := false
	for _, raw := range adminItems {
		item, _ := raw.(map[string]interface{})
		if item["id"] == "legacy-archive:"+strconv.Itoa(otherArchive.Id) {
			adminFoundOtherArchive = true
			break
		}
	}
	if !adminFoundOtherArchive {
		t.Fatal("admin history omitted another user's legacy archive")
	}

	page := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications?page=2&page_size=1&search=archive", "", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("filtered archive page status=%d, body=%s", page.Code, page.Body.String())
	}
	pageData := gatewayResponseData(t, page)
	if total, _ := pageData["total"].(float64); total != 2 {
		t.Fatalf("filtered archive count=%v, want 2", pageData["total"])
	}
	pageItems, _ := pageData["items"].([]interface{})
	if len(pageItems) != 1 {
		t.Fatalf("second archive page contains %d rows, want 1", len(pageItems))
	}
	pageItem, _ := pageItems[0].(map[string]interface{})
	if pageItem["id"] != "legacy-archive:"+strconv.Itoa(archiveOld.Id) {
		t.Fatalf("second archive page returned %v, want older archive", pageItem["id"])
	}

	detail := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications/legacy-archive:"+strconv.Itoa(archiveOld.Id), "", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("archive detail status=%d, body=%s", detail.Code, detail.Body.String())
	}
	detailData := gatewayResponseData(t, detail)
	notification, _ := detailData["notification"].(map[string]interface{})
	if notification["id"] != "legacy-archive:"+strconv.Itoa(archiveOld.Id) || notification["status"] != model.NotificationAccepted {
		t.Fatalf("archive detail was not normalized: %#v", notification)
	}
	metadataText, _ := notification["metadata"].(string)
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataText), &metadata); err != nil || metadata["archive"] != true || metadata["legacy_message_id"] != float64(archiveOld.Id) {
		t.Fatalf("archive metadata=%q parsed=%#v err=%v", metadataText, metadata, err)
	}
	deliveries, _ := detailData["deliveries"].([]interface{})
	if len(deliveries) != 0 {
		t.Fatalf("archive detail returned %d deliveries, want none", len(deliveries))
	}
	privateDetail := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications/legacy-archive:"+strconv.Itoa(otherArchive.Id), "", nil)
	if privateDetail.Code != http.StatusNotFound {
		t.Fatalf("other user's archive detail status=%d, want 404", privateDetail.Code)
	}
	bridgedArchiveDetail := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/notifications/legacy-archive:"+strconv.Itoa(bridgedMessage.Id), "", nil)
	if bridgedArchiveDetail.Code != http.StatusNotFound {
		t.Fatalf("bridged message archive detail status=%d, want 404", bridgedArchiveDetail.Code)
	}
	for _, operation := range []string{"retry", "cancel", "resend"} {
		response := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/admin/notifications/legacy-archive:"+strconv.Itoa(archiveOld.Id)+"/"+operation, "{}", nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("archive %s status=%d, want 404", operation, response.Code)
		}
	}
	if f.sender.calls != 0 {
		t.Fatalf("archive action unexpectedly reached sender %d times", f.sender.calls)
	}

	apiKeyList := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/notifications", "", nil)
	if apiKeyList.Code != http.StatusOK {
		t.Fatalf("API key notification list status=%d, body=%s", apiKeyList.Code, apiKeyList.Body.String())
	}
	apiKeyData := gatewayResponseData(t, apiKeyList)
	if total, _ := apiKeyData["total"].(float64); total != 1 {
		t.Fatalf("API key list included archive/other-source rows: total=%v", apiKeyData["total"])
	}
	legacyAPIKeyEngine := gin.New()
	legacyAPIKeyEngine.GET("/api/v1/notifications", gatewayTestAPIKeyContext(legacyKey, &legacySource), ListGatewayNotifications)
	legacyAPIKeyList := gatewayAPIRequest(t, legacyAPIKeyEngine, http.MethodGet, "/api/v1/notifications", "", nil)
	if legacyAPIKeyList.Code != http.StatusOK {
		t.Fatalf("legacy-source API key list status=%d, body=%s", legacyAPIKeyList.Code, legacyAPIKeyList.Body.String())
	}
	legacyAPIKeyData := gatewayResponseData(t, legacyAPIKeyList)
	if total, _ := legacyAPIKeyData["total"].(float64); total != 1 {
		t.Fatalf("legacy-source API key list included archive rows: total=%v, want the bridged gateway row only", legacyAPIKeyData["total"])
	}
}

func TestSessionGatewayLogsIncludeAllOwnedSourcesAndExcludeOtherOwners(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	channel := f.createChannel(t, "logs-channel", model.TypeNone, "destination")
	channelID := strconv.Itoa(channel.Id)
	sourceA := f.createSource(t, "logs-source-a", channelID)
	sourceB := f.createSource(t, "logs-source-b", channelID)
	notificationA := enqueueGatewayTestNotification(t, f.service, sourceA, channel, "logs-a")
	notificationB := enqueueGatewayTestNotification(t, f.service, sourceB, channel, "logs-b")

	otherOwnerID := 44
	if err := f.db.Create(&model.User{
		Id: otherOwnerID, Username: "logs-private-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error; err != nil {
		t.Fatal(err)
	}
	otherChannel := model.Channel{UserId: otherOwnerID, Name: "other-logs-channel", Type: model.TypeNone, Status: common.ChannelStatusEnabled}
	if err := f.db.Create(&otherChannel).Error; err != nil {
		t.Fatal(err)
	}
	otherSource, err := f.service.CreateSource(context.Background(), otherOwnerID, gateway.SourceInput{
		Name: "other-owner-source", AllowedChannelIDs: []string{strconv.Itoa(otherChannel.Id)},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherNotification := enqueueGatewayTestNotification(t, f.service, otherSource, &otherChannel, "logs-other")

	for _, notification := range []*model.Notification{notificationA, notificationB, otherNotification} {
		var delivery model.Delivery
		if err := f.db.Where("notification_id = ?", notification.Id).First(&delivery).Error; err != nil {
			t.Fatal(err)
		}
		attempt := model.DeliveryAttempt{
			Id: uuid.NewString(), DeliveryID: delivery.Id, AttemptNumber: 1,
			AttemptKind: "send", StartedAt: time.Now().UTC(), Result: model.DeliveryAccepted,
		}
		if err := f.db.Create(&attempt).Error; err != nil {
			t.Fatal(err)
		}
	}

	engine := gin.New()
	engine.GET("/api/v1/admin/logs", gatewayTestSessionContext(f.ownerID), ListGatewayLogs)
	response := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/logs?page_size=100", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("owner log query without source_id status=%d, body=%s", response.Code, response.Body.String())
	}
	data := gatewayResponseData(t, response)
	if total, _ := data["total"].(float64); total != 2 {
		t.Fatalf("owner logs total=%v, want logs from both owned sources", data["total"])
	}
	items, _ := data["items"].([]interface{})
	seenSources := map[string]bool{}
	for _, raw := range items {
		item, _ := raw.(map[string]interface{})
		sourceID, _ := item["source_id"].(string)
		seenSources[sourceID] = true
		if sourceID == otherSource.Id {
			t.Fatal("owner log query exposed another user's source")
		}
	}
	if !seenSources[sourceA.Id] || !seenSources[sourceB.Id] {
		t.Fatalf("owner log query omitted an owned source: %v", seenSources)
	}
}

func TestGatewayAPIKeyAuthRequiresBearerAndScope(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	channel := f.createChannel(t, "auth-channel", model.TypeNone, "destination")
	source := f.createSource(t, "auth-source", strconv.Itoa(channel.Id))
	_, plaintext := f.createKey(t, source.Id, "read-only", "channels:read")
	engine := gin.New()
	engine.POST("/api/v1/channels/:channelID/test", middleware.GatewayAPIKeyAuth("channels:test"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	noBearer := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/channels/1/test", "{}", map[string]string{"Cookie": "api_key=" + plaintext})
	if noBearer.Code != http.StatusUnauthorized {
		t.Fatalf("cookie credential status=%d, want 401", noBearer.Code)
	}
	wrongScope := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/channels/1/test", "{}", map[string]string{"Authorization": "Bearer " + plaintext})
	if wrongScope.Code != http.StatusForbidden {
		t.Fatalf("missing channels:test scope status=%d, want 403", wrongScope.Code)
	}
}

func TestGatewayAPIKeyPlaintextIsOnlyReturnedAtCreation(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	channel := f.createChannel(t, "key-channel", model.TypeNone, "destination")
	source := f.createSource(t, "key-source", strconv.Itoa(channel.Id))
	engine := gin.New()
	engine.POST("/api/v1/admin/sources/:sourceID/api-keys", gatewayTestSessionContext(f.ownerID), CreateGatewayAPIKey)
	engine.GET("/api/v1/admin/sources/:sourceID/api-keys", gatewayTestSessionContext(f.ownerID), ListGatewayAPIKeys)

	created := gatewayAPIRequest(t, engine, http.MethodPost, "/api/v1/admin/sources/"+source.Id+"/api-keys", `{"name":"onetime","scopes":["notifications:read"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("API key create status=%d, body=%s", created.Code, created.Body.String())
	}
	plaintext, _ := gatewayResponseData(t, created)["api_key"].(string)
	if plaintext == "" {
		t.Fatal("API key creation response omitted the one-time plaintext")
	}
	var stored model.APIKey
	if err := f.db.Where("source_id = ? AND name = ?", source.Id, "onetime").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.KeyHash == plaintext || stored.KeyHash == "" {
		t.Fatal("API key plaintext was stored instead of a one-way digest")
	}
	listed := gatewayAPIRequest(t, engine, http.MethodGet, "/api/v1/admin/sources/"+source.Id+"/api-keys", "", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("API key list status=%d, body=%s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), plaintext) || strings.Contains(listed.Body.String(), stored.KeyHash) {
		t.Fatal("API key listing exposed credential material")
	}
}

func TestGatewayChannelTestIsSourceScopedAuditedAndMarkedTest(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	allowedChannel := f.createChannel(t, "qq-active", model.TypeQQBotV2, "user-openid")
	unallowedChannel := f.createChannel(t, "not-allowed", model.TypeNone, "destination")
	sourceA := f.createSource(t, "test-source-a", strconv.Itoa(allowedChannel.Id))
	sourceB := f.createSource(t, "test-source-b", strconv.Itoa(allowedChannel.Id))
	keyA, _ := f.createKey(t, sourceA.Id, "test-key", "channels:test")
	engine := gin.New()
	engine.POST("/api/v1/channels/:channelID/test", gatewayTestAPIKeyContext(keyA, sourceA), TestGatewayChannel)

	body := fmt.Sprintf(`{"source_id":%q,"title":"Test","content":"active push","delivery_mode":"active"}`, sourceA.Id)
	response := gatewayAPIRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/channels/%d/test", allowedChannel.Id), body, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("ordinary active QQBot test status=%d, body=%s", response.Code, response.Body.String())
	}
	data := gatewayResponseData(t, response)
	deliveryID, _ := data["delivery_id"].(string)
	notificationID, _ := data["notification_id"].(string)
	if data["is_test"] != true || deliveryID == "" || notificationID == "" {
		t.Fatalf("test response omitted test/log identity fields: %#v", data)
	}
	if f.sender.calls != 1 {
		t.Fatalf("sender calls=%d, want one active test send", f.sender.calls)
	}
	var notification model.Notification
	if err := f.db.Where("id = ?", notificationID).First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if !notification.IsTest || notification.SourceID != sourceA.Id {
		t.Fatalf("test notification scope/marker mismatch: %+v", notification)
	}
	var audit model.AuditLog
	if err := f.db.Where("source_id = ? AND action = ?", sourceA.Id, "channel.test_send").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorType != "api_key" || audit.ActorID != "api-key:"+keyA.Id || audit.ResourceID != deliveryID {
		t.Fatalf("test-send audit attribution mismatch: %+v", audit)
	}

	wrongSourceBody := fmt.Sprintf(`{"source_id":%q,"content":"must reject"}`, sourceB.Id)
	wrongSource := gatewayAPIRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/channels/%d/test", allowedChannel.Id), wrongSourceBody, nil)
	if wrongSource.Code != http.StatusForbidden {
		t.Fatalf("API key source override status=%d, want 403", wrongSource.Code)
	}
	unallowedBody := fmt.Sprintf(`{"source_id":%q,"content":"must reject"}`, sourceA.Id)
	unallowed := gatewayAPIRequest(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/channels/%d/test", unallowedChannel.Id), unallowedBody, nil)
	if unallowed.Code != http.StatusForbidden {
		t.Fatalf("source channel allowlist status=%d, want 403", unallowed.Code)
	}
	if f.sender.calls != 1 {
		t.Fatalf("rejected test requests reached sender; calls=%d", f.sender.calls)
	}
}

func TestGatewayTargetUpdateCannotCrossSourceOrForgeInteraction(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	channelA := f.createChannel(t, "target-channel-a", model.TypeNone, "destination-a")
	channelB := f.createChannel(t, "target-channel-b", model.TypeNone, "destination-b")
	channelAID, channelBID := strconv.Itoa(channelA.Id), strconv.Itoa(channelB.Id)
	sourceA := f.createSource(t, "target-source-a", channelAID)
	sourceB := f.createSource(t, "target-source-b", channelAID)
	targetA, err := f.service.UpsertTarget(context.Background(), f.ownerID, gateway.TargetInput{
		SourceID: sourceA.Id, ChannelID: channelAID, TargetType: "user", ExternalID: "target-a", Name: "target-a", Status: model.GatewayStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := f.service.UpsertTarget(context.Background(), f.ownerID, gateway.TargetInput{
		SourceID: sourceB.Id, ChannelID: channelAID, TargetType: "user", ExternalID: "target-b", Name: "target-b", Status: model.GatewayStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.PATCH("/api/v1/admin/sources/:sourceID/targets/:targetID", gatewayTestSessionContext(f.ownerID), UpdateGatewayTarget)

	crossSource := gatewayAPIRequest(t, engine, http.MethodPatch, "/api/v1/admin/sources/"+sourceA.Id+"/targets/"+targetB.Id, `{"name":"hijacked"}`, nil)
	if crossSource.Code != http.StatusNotFound {
		t.Fatalf("cross-source target update status=%d, want 404", crossSource.Code)
	}
	moveToForbiddenChannel := gatewayAPIRequest(t, engine, http.MethodPatch, "/api/v1/admin/sources/"+sourceA.Id+"/targets/"+targetA.Id, fmt.Sprintf(`{"channel_id":%q}`, channelBID), nil)
	if moveToForbiddenChannel.Code != http.StatusForbidden {
		t.Fatalf("target move outside source allowlist status=%d, want 403", moveToForbiddenChannel.Code)
	}
	forgedInteraction := gatewayAPIRequest(t, engine, http.MethodPatch, "/api/v1/admin/sources/"+sourceA.Id+"/targets/"+targetA.Id, `{"last_interaction_at":"2099-01-01T00:00:00Z"}`, nil)
	if forgedInteraction.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied interaction timestamp status=%d, want 400", forgedInteraction.Code)
	}
	var unchanged model.Target
	if err := f.db.Where("id = ?", targetB.Id).First(&unchanged).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Name != "target-b" || unchanged.SourceID != sourceB.Id {
		t.Fatalf("cross-source target was modified: %+v", unchanged)
	}
	var unchangedTargetA model.Target
	if err := f.db.Where("id = ?", targetA.Id).First(&unchangedTargetA).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedTargetA.ChannelID != channelAID {
		t.Fatalf("rejected target channel change persisted: %+v", unchangedTargetA)
	}
}

func TestLatestVerifiedQQBotInteractionUsesModeSpecificWindowsAndEvents(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	configured := f.createChannel(t, "qq-interactions", model.TypeQQBotV2, "user-openid")
	now := time.Now().UTC()
	channelID := strconv.Itoa(configured.Id)
	stale := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "stale-event", EventType: "C2C_MESSAGE_CREATE",
		MessageID: "stale-message", TargetType: "user", TargetOpenID: "user-openid", Status: "processed", ReceivedAt: now.Add(-6 * time.Minute),
	}
	if err := f.db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := latestVerifiedQQBotInteraction(context.Background(), configured, "user", "user-openid", "passive"); !errors.Is(err, errGatewayInteractionRequired) {
		t.Fatalf("stale QQBot interaction error=%v, want interaction required", err)
	}
	valid := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "valid-event", EventType: "C2C_MESSAGE_CREATE",
		MessageID: "valid-message", MessageSequence: 27, TargetType: "user", TargetOpenID: "user-openid",
		Status: "processed", ReceivedAt: now.Add(-time.Minute),
	}
	if err := f.db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err := latestVerifiedQQBotInteraction(context.Background(), configured, "user", "user-openid", "passive")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.EventID != valid.EventID || metadata.MessageID != valid.MessageID || metadata.MessageSeq != 0 || metadata.Mode != "passive" {
		t.Fatalf("passive metadata did not use verified callback fields safely: %+v", metadata)
	}
	wakeup := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "wakeup-event", EventType: "C2C_MESSAGE_CREATE",
		MessageID: "wakeup-message", TargetType: "user", TargetOpenID: "wakeup-user", Status: "processed", ReceivedAt: now.Add(-10 * 24 * time.Hour),
	}
	if err := f.db.Create(&wakeup).Error; err != nil {
		t.Fatal(err)
	}
	friendAdded := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "friend-added-event", EventType: "FRIEND_ADD",
		TargetType: "user", TargetOpenID: "wakeup-user", Status: "processed", ReceivedAt: now.Add(-30 * time.Second),
	}
	if err := f.db.Create(&friendAdded).Error; err != nil {
		t.Fatal(err)
	}
	received := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "received-event", EventType: "C2C_MSG_RECEIVE",
		TargetType: "user", TargetOpenID: "wakeup-user", Status: "processed", ReceivedAt: now.Add(-10 * time.Second),
	}
	if err := f.db.Create(&received).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err = latestVerifiedQQBotInteraction(context.Background(), configured, "user", "wakeup-user", "wakeup")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.EventID != wakeup.EventID || metadata.MessageID != "" || metadata.Mode != "wakeup" || !metadata.IsWakeUp {
		t.Fatalf("wakeup metadata was not built from a verified C2C event: %+v", metadata)
	}
	interaction := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "interaction-event", EventType: "INTERACTION_CREATE",
		TargetType: "user", TargetOpenID: "interaction-user", Status: "processed", ReceivedAt: now.Add(-2 * 24 * time.Hour),
	}
	if err := f.db.Create(&interaction).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err = latestVerifiedQQBotInteraction(context.Background(), configured, "user", "interaction-user", "wakeup")
	if err != nil || metadata.EventID != interaction.EventID {
		t.Fatalf("verified C2C interaction was not accepted for wakeup: metadata=%+v err=%v", metadata, err)
	}
	tooOld := model.GatewayEvent{
		Id: uuid.NewString(), ChannelID: channelID, EventID: "expired-event", EventType: "C2C_MESSAGE_CREATE",
		TargetType: "user", TargetOpenID: "expired-user", Status: "processed", ReceivedAt: now.Add(-31 * 24 * time.Hour),
	}
	if err := f.db.Create(&tooOld).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := latestVerifiedQQBotInteraction(context.Background(), configured, "user", "expired-user", "wakeup"); !errors.Is(err, errGatewayInteractionRequired) {
		t.Fatalf("expired wakeup interaction error=%v, want interaction required", err)
	}
	if _, err := latestVerifiedQQBotInteraction(context.Background(), configured, "group", "group-openid", "wakeup"); err == nil {
		t.Fatal("group target was accepted for wakeup")
	}
}

func TestGatewayQQBotCallbackPreservesManualTargetStateAndInteractionTime(t *testing.T) {
	f := newGatewayAPITestFixture(t)
	configured := f.createChannel(t, "qq-callback", model.TypeQQBotV2, "")
	channelID := strconv.Itoa(configured.Id)
	source := f.createSource(t, "callback-source", channelID)
	oldInteraction := time.Now().UTC().Add(-time.Hour)
	target := model.Target{
		Id: uuid.NewString(), SourceID: source.Id, ChannelID: channelID, TargetType: "user", ExternalID: "known-user",
		Name: "known-user", Source: "manual", Status: model.GatewayStatusDisabled, LastInteractionAt: &oldInteraction,
	}
	if err := f.db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	callbackTime := time.Now().UTC()
	interaction := channel.QQBotCallbackEvent{
		EventID: "incoming-event", EventType: "C2C_MESSAGE_CREATE", TargetType: "user", TargetOpenID: "known-user",
		MessageID: "incoming-message", MessageSequence: 99, TargetInteraction: true, ReceivedAt: callbackTime,
	}
	if err := persistGatewayQQBotEvent(context.Background(), configured, interaction); err != nil {
		t.Fatal(err)
	}
	var refreshed model.Target
	if err := f.db.Where("id = ?", target.Id).First(&refreshed).Error; err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != model.GatewayStatusDisabled || refreshed.LastInteractionAt == nil || !refreshed.LastInteractionAt.Equal(callbackTime) {
		t.Fatalf("callback re-enabled or misdated a manually disabled target: %+v", refreshed)
	}

	discovery := channel.QQBotCallbackEvent{
		EventID: "friend-add-event", EventType: "FRIEND_ADD", TargetType: "user", TargetOpenID: "discovered-user",
		ReceivedAt: callbackTime,
	}
	if err := persistGatewayQQBotEvent(context.Background(), configured, discovery); err != nil {
		t.Fatal(err)
	}
	var discovered model.Target
	if err := f.db.Where("source_id = ? AND external_id = ?", source.Id, "discovered-user").First(&discovered).Error; err != nil {
		t.Fatal(err)
	}
	if discovered.Status != model.GatewayStatusActive || discovered.LastInteractionAt != nil {
		t.Fatalf("friend discovery fabricated an interaction timestamp: %+v", discovered)
	}

	removed := channel.QQBotCallbackEvent{
		EventID: "friend-removed-event", EventType: "FRIEND_DEL", TargetType: "user", TargetOpenID: "discovered-user",
		TargetRemoved: true, ReceivedAt: callbackTime.Add(time.Minute),
	}
	if err := persistGatewayQQBotEvent(context.Background(), configured, removed); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("id = ?", discovered.Id).First(&discovered).Error; err != nil {
		t.Fatal(err)
	}
	if discovered.Status != model.GatewayStatusDisabled || discovered.LastInteractionAt != nil {
		t.Fatalf("removal event did not disable target safely: %+v", discovered)
	}
	var storedEvent model.GatewayEvent
	if err := f.db.Where("channel_id = ? AND event_id = ?", channelID, interaction.EventID).First(&storedEvent).Error; err != nil {
		t.Fatal(err)
	}
	if storedEvent.MessageSequence != interaction.MessageSequence || storedEvent.EventType != interaction.EventType || storedEvent.MessageID != interaction.MessageID {
		t.Fatalf("callback envelope/event metadata was not persisted distinctly: %+v", storedEvent)
	}
}
