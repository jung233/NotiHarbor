package model

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"message-pusher/common"
	"os"
	"time"
)

var DB *gorm.DB

type safeDBLogger struct{}

func (safeDBLogger) LogMode(logger.LogLevel) logger.Interface     { return safeDBLogger{} }
func (safeDBLogger) Info(context.Context, string, ...interface{}) {}
func (safeDBLogger) Warn(_ context.Context, format string, args ...interface{}) {
	common.SysError(fmt.Sprintf(format, args...))
}
func (safeDBLogger) Error(_ context.Context, format string, args ...interface{}) {
	common.SysError(fmt.Sprintf(format, args...))
}
func (safeDBLogger) Trace(_ context.Context, begin time.Time, _ func() (string, int64), err error) {
	// Never evaluate the SQL callback: older GORM interpolates credential values.
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.SysError("database query failed")
	} else if time.Since(begin) > time.Second {
		common.SysLog("database query exceeded one second")
	}
}

func createRootAccountIfNeed() error {
	var user User
	if err := DB.First(&user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		username, password := os.Getenv("INITIAL_ADMIN_USERNAME"), os.Getenv("INITIAL_ADMIN_PASSWORD")
		if username == "" {
			username = "root"
		}
		if len(password) < 12 || password == "123456" {
			return errors.New("first startup requires INITIAL_ADMIN_PASSWORD of at least 12 characters")
		}
		hashedPassword, err := common.Password2Hash(password)
		if err != nil {
			return err
		}
		rootUser := User{
			Username:    username,
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
		}
		return DB.Create(&rootUser).Error
	}
	var legacyRoot User
	err := DB.Where("username = ? AND role = ?", "root", common.RoleRootUser).First(&legacyRoot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if common.ValidatePasswordAndHash("123456", legacyRoot.Password) {
		password := os.Getenv("INITIAL_ADMIN_PASSWORD")
		if len(password) < 12 {
			return errors.New("legacy root account still uses the insecure default password; set INITIAL_ADMIN_PASSWORD of at least 12 characters to rotate it")
		}
		hash, err := common.Password2Hash(password)
		if err != nil {
			return err
		}
		return DB.Model(&legacyRoot).Update("password", hash).Error
	}
	return nil
}

func CountTable(tableName string) (num int64) {
	DB.Table(tableName).Count(&num)
	return
}

func InitDB() (err error) {
	var db *gorm.DB
	if os.Getenv("SQL_DSN") != "" {
		// Use MySQL
		db, err = gorm.Open(mysql.Open(os.Getenv("SQL_DSN")), &gorm.Config{
			PrepareStmt: true, // precompile SQL
			Logger:      safeDBLogger{},
		})
	} else {
		// Use SQLite
		db, err = gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{
			PrepareStmt: true, // precompile SQL
			Logger:      safeDBLogger{},
		})
		common.SysLog("SQL_DSN not set, using SQLite as database")
	}
	if err == nil {
		DB = db
		err := db.AutoMigrate(&User{})
		if err != nil {
			return err
		}
		err = db.AutoMigrate(&Option{})
		if err != nil {
			return err
		}
		err = db.AutoMigrate(&Message{})
		if err != nil {
			return err
		}
		err = db.AutoMigrate(&Channel{})
		if err != nil {
			return err
		}
		err = db.AutoMigrate(&Webhook{})
		if err != nil {
			return err
		}
		err = createRootAccountIfNeed()
		return err
	} else {
		common.FatalLog(err)
	}
	return err
}

func CloseDB() error {
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	return err
}
