package model

import (
	"message-pusher/common"
	"strings"

	"gorm.io/gorm"
)

// MigrateStoredCredentials is an idempotent, non-destructive upgrade. Keep the
// encryption key and a database backup together before running the new image.
func MigrateStoredCredentials(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		type channelCredential struct {
			Id     int
			Secret string
			Token  *string
		}
		var rows []channelCredential
		if err := tx.Table("channels").Select("id", "secret", "token").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			updates := map[string]interface{}{}
			if row.Secret != "" && !strings.HasPrefix(row.Secret, "enc:v1:") {
				value, err := common.EncryptCredential(row.Secret)
				if err != nil {
					return err
				}
				updates["secret"] = value
			}
			if row.Token != nil && *row.Token != "" && !strings.HasPrefix(*row.Token, "enc:v1:") {
				value, err := common.EncryptCredential(*row.Token)
				if err != nil {
					return err
				}
				updates["token"] = value
			}
			if len(updates) > 0 {
				if err := tx.Table("channels").Where("id = ?", row.Id).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		type optionCredential struct {
			Key   string
			Value string
		}
		var options []optionCredential
		if err := tx.Table("options").Find(&options).Error; err != nil {
			return err
		}
		for _, option := range options {
			if sensitiveOption(option.Key) && option.Value != "" && !strings.HasPrefix(option.Value, "enc:v1:") {
				value, err := common.EncryptCredential(option.Value)
				if err != nil {
					return err
				}
				if err := tx.Table("options").Where("`key` = ?", option.Key).Update("value", value).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
