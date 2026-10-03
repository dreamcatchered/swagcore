package hub

import "errors"

var (
	ErrNodeOffline   = errors.New("node offline")
	ErrSendQueueFull = errors.New("send queue full")
)
