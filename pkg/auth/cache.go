package auth

import (
	"sync"
	"time"
)

// TODO redis
var Cache sync.Map

type UserInfo struct {
	Name      string
	Namespace string
	RenewTime time.Time
}
