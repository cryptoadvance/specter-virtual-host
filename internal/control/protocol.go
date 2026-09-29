package control

import "encoding/json"

type Request struct {
	Command   string            `json:"command"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}
