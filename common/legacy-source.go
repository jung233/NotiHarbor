package common

import (
	"github.com/google/uuid"
	"strconv"
)

func LegacySourceID(userID int) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("message-pusher:legacy:user:"+strconv.Itoa(userID))).String()
}
