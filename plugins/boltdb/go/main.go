package api

import (
	"github.com/go-flutter-desktop/go-flutter"
	"github.com/go-flutter-desktop/go-flutter/plugin"
	"github.com/go-flutter-desktop/plugins/boltdb/api/boltdb"
)

type DB struct {
	channel *plugin.MethodChannel
}

var _ flutter.Plugin = &DB{}

func (p *DB) InitPlugin(messenger plugin.BinaryMessenger) error {
	p.channel = plugin.NewMethodChannel(messenger, "boltdb", plugin.StandardMethodCodec{})
	p.channel.HandleFunc("boltDB", getRemotesFunc)
	return nil
}

func getRemotesFunc(arguments interface{}) (reply interface{}, err error) {
	return boltdb.BoltDB(arguments.(string)), nil
}
