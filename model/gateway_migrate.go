package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// MigrateGateway creates and upgrades the versioned gateway schema. It deliberately
// touches only gateway-owned tables so existing Message Pusher data is preserved.
func MigrateGateway(db *gorm.DB) error {
	if db == nil {
		return errors.New("gateway migration requires a database")
	}

	if !db.Migrator().HasTable(&GatewaySchemaVersion{}) {
		if err := db.Migrator().CreateTable(&GatewaySchemaVersion{}); err != nil {
			return err
		}
	}

	var current GatewaySchemaVersion
	err := db.Order("version DESC").First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if current.Version >= 1 {
		return nil
	}

	// AutoMigrate is repeatable if a DDL operation is interrupted. The version is
	// recorded only after every gateway table and index has been created.
	migrator := db
	if db.Dialector != nil && db.Dialector.Name() == "mysql" {
		migrator = db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
	}
	if err := migrator.AutoMigrate(
		&Notification{},
		&Delivery{},
		&DeliveryAttempt{},
		&OutboxEvent{},
		&Source{},
		&APIKey{},
		&Target{},
		&AuditLog{},
		&GatewayEvent{},
		&QQBotMessageSequence{},
	); err != nil {
		return err
	}

	return db.Create(&GatewaySchemaVersion{Version: 1, AppliedAt: time.Now().UTC()}).Error
}
