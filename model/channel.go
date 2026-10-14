package model

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"message-pusher/common"
)

const (
	TypeEmail             = "email"
	TypeWeChatTestAccount = "test"
	TypeWeChatCorpAccount = "corp_app"
	TypeCorp              = "corp"
	TypeLark              = "lark"
	TypeDing              = "ding"
	TypeTelegram          = "telegram"
	TypeDiscord           = "discord"
	TypeBark              = "bark"
	TypeClient            = "client"
	TypeNone              = "none"
	TypeOneBot            = "one_bot"
	TypeGroup             = "group"
	TypeLarkApp           = "lark_app"
	TypeCustom            = "custom"
	TypeTencentAlarm      = "tencent_alarm"
	TypeQQBotV2           = "qqbot_v2"
)

type Channel struct {
	Id          int     `json:"id"`
	Type        string  `json:"type" gorm:"type:varchar(32)"`
	UserId      int     `json:"user_id" gorm:"uniqueIndex:name_user_id;index"`
	Name        string  `json:"name" gorm:"type:varchar(32);uniqueIndex:name_user_id"`
	Description string  `json:"description"`
	Status      int     `json:"status" gorm:"default:1"` // enabled, disabled
	Secret      string  `json:"secret" gorm:"index"`
	AppId       string  `json:"app_id"`
	AccountId   string  `json:"account_id"`
	URL         string  `json:"url" gorm:"column:url"`
	Other       string  `json:"other"`
	CreatedTime int64   `json:"created_time" gorm:"bigint"`
	Token       *string `json:"token" gorm:"token"`
}

// Secrets are encrypted on new writes; existing plaintext remains readable.
func (channel *Channel) BeforeSave(tx *gorm.DB) error {
	var err error
	channel.Secret, err = common.EncryptCredential(channel.Secret)
	if err != nil {
		return err
	}
	if channel.Token != nil {
		value, err := common.EncryptCredential(*channel.Token)
		if err != nil {
			return err
		}
		channel.Token = &value
	}
	return nil
}

func (channel *Channel) AfterFind(tx *gorm.DB) error {
	var err error
	channel.Secret, err = common.DecryptCredential(channel.Secret)
	if err != nil {
		return err
	}
	if channel.Token != nil {
		value, err := common.DecryptCredential(*channel.Token)
		if err != nil {
			return err
		}
		channel.Token = &value
	}
	return nil
}

func (channel *Channel) AfterSave(tx *gorm.DB) error { return channel.AfterFind(tx) }

func (channel Channel) MarshalJSON() ([]byte, error) {
	type publicChannel Channel
	channel.Secret = ""
	channel.Token = nil
	return json.Marshal(publicChannel(channel))
}

// Ciphertexts have random nonces, so shared credentials cannot be compared in SQL.
func ChannelCredentialShared(appID, channelType, secret string) bool {
	var channels []Channel
	if err := DB.Where("app_id = ? AND type = ?", appID, channelType).Find(&channels).Error; err != nil {
		common.SysError("failed to inspect shared channel credentials")
		return true // Do not evict another channel's token on an uncertain lookup.
	}
	count := 0
	for _, configured := range channels {
		if configured.Secret == secret {
			count++
		}
	}
	return count > 1
}

type BriefChannel struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func GetChannelById(id int, userId int, selectAll bool) (*Channel, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	c := Channel{Id: id, UserId: userId}
	var err error
	if selectAll {
		err = DB.Where(c).First(&c).Error
	} else {
		err = DB.Omit("secret").Where(c).First(&c).Error
	}
	return &c, err
}

func GetChannelByName(name string, userId int) (*Channel, error) {
	if name == "" || userId == 0 {
		return nil, errors.New("name 或 userId 为空！")
	}
	c := Channel{Name: name, UserId: userId}
	err := DB.Where(c).First(&c).Error
	return &c, err
}

func GetTokenStoreChannels() (channels []*Channel, err error) {
	err = DB.Where("type in ?", []string{TypeWeChatCorpAccount, TypeWeChatTestAccount, TypeLarkApp}).Find(&channels).Error
	return channels, err
}

func GetTokenStoreChannelsByUserId(userId int) (channels []*Channel, err error) {
	err = DB.Where("user_id = ?", userId).Where("type = ? or type = ?", TypeWeChatCorpAccount, TypeWeChatTestAccount).Find(&channels).Error
	return channels, err
}

func GetChannelsByUserId(userId int, startIdx int, num int) (channels []*Channel, err error) {
	err = DB.Omit("secret").Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&channels).Error
	return channels, err
}

func GetBriefChannelsByUserId(userId int) (channels []*BriefChannel, err error) {
	err = DB.Model(&Channel{}).Select("id", "name", "description").Where("user_id = ? and status = ?", userId, common.ChannelStatusEnabled).Find(&channels).Error
	return channels, err
}

func SearchChannels(userId int, keyword string) (channels []*Channel, err error) {
	err = DB.Omit("secret").Where("user_id = ?", userId).Where("id = ? or name LIKE ?", keyword, keyword+"%").Find(&channels).Error
	return channels, err
}

func DeleteChannelById(id int, userId int) (c *Channel, err error) {
	// Why we need userId here? In case user want to delete other's c.
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	c = &Channel{Id: id, UserId: userId}
	err = DB.Where(c).First(&c).Error
	if err != nil {
		return nil, err
	}
	return c, c.Delete()
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	return err
}

func (channel *Channel) UpdateStatus(status int) error {
	err := DB.Model(channel).Update("status", status).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (channel *Channel) Update() error {
	var err error
	err = DB.Model(channel).Select("type", "name", "description", "secret", "app_id", "account_id", "url", "other", "status", "token").Updates(channel).Error
	return err
}

func (channel *Channel) Delete() error {
	err := DB.Delete(channel).Error
	return err
}
