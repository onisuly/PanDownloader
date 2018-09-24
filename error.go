package main

type panError struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
	RequestID int64  `json:"request_id"`
}
