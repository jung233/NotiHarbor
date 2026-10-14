package middleware

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"message-pusher/common"
	"message-pusher/model"
	"net/http"
)

func authHelper(c *gin.Context, minRole int) {
	session := sessions.Default(c)
	username := session.Get("username")
	role := session.Get("role")
	id := session.Get("id")
	status := session.Get("status")
	usernameString, usernameOK := username.(string)
	roleInt, roleOK := role.(int)
	idInt, idOK := id.(int)
	statusInt, statusOK := status.(int)
	if !usernameOK || usernameString == "" || !roleOK || !idOK || idInt <= 0 || !statusOK {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "无权进行此操作，未登录",
		})
		c.Abort()
		return
	}
	if statusInt != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已被封禁",
		})
		c.Abort()
		return
	}
	if roleInt < minRole {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权进行此操作，权限不足",
		})
		c.Abort()
		return
	}
	currentUser, err := model.GetUserById(idInt, false)
	if err != nil || currentUser.Status != common.UserStatusEnabled || currentUser.Role < minRole {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "会话身份已失效"})
		return
	}
	c.Set("username", username)
	c.Set("role", currentUser.Role)
	c.Set("id", id)
	c.Next()
}

func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleCommonUser)
	}
}

func AdminAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleAdminUser)
	}
}

func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleRootUser)
	}
}
