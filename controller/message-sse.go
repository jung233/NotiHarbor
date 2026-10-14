package controller

import (
	"github.com/gin-gonic/gin"
	"io"
	"message-pusher/model"
	"sync"
)

var messageChanBufferSize = 10

var messageChanStore struct {
	Map   map[int]*chan *model.Message
	Mutex sync.RWMutex
}

func messageChanStoreAdd(messageChan *chan *model.Message, userId int) {
	messageChanStore.Mutex.Lock()
	defer messageChanStore.Mutex.Unlock()
	messageChanStore.Map[userId] = messageChan
}

func messageChanStoreRemove(userId int) {
	messageChanStore.Mutex.Lock()
	defer messageChanStore.Mutex.Unlock()
	delete(messageChanStore.Map, userId)
}

func init() {
	messageChanStore.Map = make(map[int]*chan *model.Message)
}

func syncMessageToUser(message *model.Message, userId int) {
	messageChanStore.Mutex.RLock()
	defer messageChanStore.Mutex.RUnlock()
	messageChan, ok := messageChanStore.Map[userId]
	if !ok {
		return
	}
	select {
	case *messageChan <- message:
	default:
	} // History remains durable when a slow browser falls behind.
}

func GetNewMessages(c *gin.Context) {
	userId := c.GetInt("id")
	messageChan := make(chan *model.Message, messageChanBufferSize)
	messageChanStoreAdd(&messageChan, userId)
	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case msg, ok := <-messageChan:
			if ok {
				c.SSEvent("message", *msg)
				return true
			}
		}
		return false
	})
	messageChanStoreRemove(userId)
	close(messageChan)
}
