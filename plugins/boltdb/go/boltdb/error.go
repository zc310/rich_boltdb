package boltdb

import "github.com/valyala/fastjson"

type rpcError struct {
	Code    int
	Message string
}

func newError(err error) *rpcError {
	return &rpcError{Code: 0, Message: err.Error()}
}
func newResult(v *fastjson.Value, err *rpcError, result *fastjson.Value) string {
	var aa fastjson.Arena
	o := aa.NewObject()

	var b []byte
	if v.Exists("jsonrpc") {
		o.Set("jsonrpc", v.Get("jsonrpc"))
	}

	if v.Exists("id") {
		o.Set("id", v.Get("id"))
	}

	if err == nil {
		o.Set("result", result)

	} else {
		n := aa.NewObject()
		n.Set("code", aa.NewNumberInt(err.Code))
		n.Set("message", aa.NewString(err.Message))
		o.Set("error", n)
	}

	b = o.MarshalTo(b[:0])
	return string(b)
}
