package controller

import (
	"testing"

	"message-pusher/model"
)

func TestMessageStreamsFanOutAndRemoveIndependently(t *testing.T) {
	const userID = 984231
	firstStream := make(chan *model.Message, 1)
	secondStream := make(chan *model.Message, 1)
	messageChanStoreAdd(firstStream, userID)
	messageChanStoreAdd(secondStream, userID)
	t.Cleanup(func() {
		messageChanStoreRemove(firstStream, userID)
		messageChanStoreRemove(secondStream, userID)
		close(firstStream)
		close(secondStream)
	})

	firstMessage := &model.Message{Id: 1}
	syncMessageToUser(firstMessage, userID)
	if got := <-firstStream; got != firstMessage {
		t.Fatalf("first stream received %p, want %p", got, firstMessage)
	}
	if got := <-secondStream; got != firstMessage {
		t.Fatalf("second stream received %p, want %p", got, firstMessage)
	}

	messageChanStoreRemove(firstStream, userID)
	secondMessage := &model.Message{Id: 2}
	syncMessageToUser(secondMessage, userID)
	select {
	case got := <-firstStream:
		t.Fatalf("removed stream unexpectedly received %p", got)
	default:
	}
	if got := <-secondStream; got != secondMessage {
		t.Fatalf("remaining stream received %p, want %p", got, secondMessage)
	}
}
