// Copyright (c) 2016 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package zapcore

import (
	"encoding/json"
	"fmt"
	"github.com/Byfengfeng/es/esService"
	"github.com/olivere/elastic/v7"
	"net"
	"strings"
	"time"
)

// Core is a minimal, fast logger interface. It's designed for library authors
// to wrap in a more user-friendly API.
type Core interface {
	LevelEnabler

	// With adds structured context to the Core.
	With([]Field) Core
	// Check determines whether the supplied Entry should be logged (using the
	// embedded LevelEnabler and possibly some extra logic). If the entry
	// should be logged, the Core adds itself to the CheckedEntry and returns
	// the result.
	//
	// Callers must use Check before calling Write.
	Check(Entry, *CheckedEntry) *CheckedEntry
	// Write serializes the Entry and any Fields supplied at the log site and
	// writes them to their destination.
	//
	// If called, Write should always log the Entry and Fields; it should not
	// replicate the logic of Check.
	Write(Entry, []Field) error
	// Sync flushes buffered logs (if any).
	Sync() error
}

type nopCore struct{}

// NewNopCore returns a no-op Core.
func NewNopCore() Core                                        { return nopCore{} }
func (nopCore) Enabled(Level) bool                            { return false }
func (n nopCore) With([]Field) Core                           { return n }
func (nopCore) Check(_ Entry, ce *CheckedEntry) *CheckedEntry { return ce }
func (nopCore) Write(Entry, []Field) error                    { return nil }
func (nopCore) Sync() error                                   { return nil }

// NewCore creates a Core that writes logs to a WriteSyncer.
func NewCore(enc Encoder, ws WriteSyncer, enab LevelEnabler,es bool,esClient *elastic.Client) Core {
	return &ioCore{
		LevelEnabler: enab,
		enc:          enc,
		out:          ws,
		outEs:		  es,
		EsClient:	  esClient,
	}
}

type ioCore struct {
	LevelEnabler
	enc Encoder
	out WriteSyncer
	outEs bool
	EsClient *elastic.Client
}

func (c *ioCore) With(fields []Field) Core {
	clone := c.clone()
	addFields(clone.enc, fields)
	return clone
}

func (c *ioCore) Check(ent Entry, ce *CheckedEntry) *CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *ioCore) Write(ent Entry, fields []Field) error {
	buf, err := c.enc.EncodeEntry(ent, fields)
	if err != nil {
		return err
	}
	_, err = c.out.Write(buf.Bytes())

	buf.Free()
	if err != nil {
		return err
	}
	if ent.Level > ErrorLevel {
		// Since we may be crashing the program, sync the output. Ignore Sync
		// errors, pending a clean solution to issue #370.
		c.Sync()
	}
	if c.outEs && c.EsClient != nil {
		SyncEs(string(buf.Bytes()),c.EsClient)
	}
	return nil
}

func SyncEs(text string,esClient *elastic.Client)  {
	file := strings.Split(text, "\t")
	Log := Log{}
	parseTime, _ := time.ParseInLocation("2006-01-02 15:04:05.999", file[0], time.Local)
	Log.Time = parseTime.UnixNano() / 1e6
	Log.LogLevel = file[1]
	Log.Src = file[2]
	Log.ServerName = file[3]
	Log.Data = file[4]
	if Log.ServerName == "trace"{
		reqData := ReqData{}
		json.Unmarshal([]byte(file[4]),&reqData)
		Log.Uid = reqData.Uid
		Log.Tid = reqData.Tid
		Log.Parent = reqData.Parent
		esService.Save(esClient,Log,Trace,"log",Log.Time)
	}else {
		esService.Save(esClient,Log,Ordinary,"log",Log.Time)
	}
}


var Ordinary = "普通日志"+GetHost()
var Trace = "链路日志"+GetHost()

type Log struct {
	Time       int64  `json:"time"`
	LogLevel   string `json:"log_level"`
	Src        string `json:"src"`
	ServerName string `json:"server_name"`
	Uid        int64  `json:"uid"`
	Tid        int64 `json:"tid"`
	Parent 	   int64 `json:"parent"`
	Data    string `json:"req_data"`
}

type ReqData struct{
	Uid        int64  `json:"uid"`
	Tid        int64 `json:"tid"`
	Parent 	   int64 `json:"parent"`
}

func GetHost()  string{
	addrs, err := net.InterfaceAddrs()
	ip:=""
	if err != nil{
		fmt.Println(err)
		return ""
	}
	for _, value := range addrs{
		if ipnet, ok := value.(*net.IPNet); ok && !ipnet.IP.IsLoopback(){
			if ipnet.IP.To4() != nil{
				ip = ipnet.IP.String()
			}
		}
	}
	return ip
}

func (c *ioCore) Sync() error {
	return c.out.Sync()
}

func (c *ioCore) clone() *ioCore {
	return &ioCore{
		LevelEnabler: c.LevelEnabler,
		enc:          c.enc.Clone(),
		out:          c.out,
		outEs:		  c.outEs,
	}
}