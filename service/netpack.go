package service

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MESSAGE_VERSION   = 1 // 消息协议版本
	MESSAGE_HEAD_SIZE = 7 // 消息头部大小
)

// 消息包结构
type Message struct {
	Version  uint8  // 消息协议版本
	Topic    uint8  // 消息主题
	SubTopic uint8  // 消息子主题
	Length   uint32 // 消息内容长度

	Body []byte //消息体
}

func NewMessage(topic, subtopic uint8) *Message {
	return &Message{
		Version:  MESSAGE_VERSION,
		Topic:    topic,
		SubTopic: subtopic,
		Body:     make([]byte, 0),
	}
}

func (msg *Message) Encode() []byte {
	length := MESSAGE_HEAD_SIZE + len(msg.Body)
	buf := make([]byte, length)
	buf[0] = msg.Version
	buf[1] = msg.Topic
	buf[2] = msg.SubTopic
	binary.LittleEndian.PutUint32(buf[3:MESSAGE_HEAD_SIZE], uint32(length))
	copy(buf[MESSAGE_HEAD_SIZE:], msg.Body)
	return buf
}

func (msg *Message) Decode(buf []byte) (err error) {
	if len(buf) < MESSAGE_HEAD_SIZE {
		return errors.New("unsupported message")
	}
	msg.Version = uint8(buf[0])
	if msg.Version != MESSAGE_VERSION {
		return fmt.Errorf("unsupported message version v%d", msg.Version)
	}
	msg.Topic = uint8(buf[1])
	msg.SubTopic = uint8(buf[2])
	msg.Length = binary.LittleEndian.Uint32(buf[3:MESSAGE_HEAD_SIZE])

	msg.Body = buf[MESSAGE_HEAD_SIZE:msg.Length]
	return
}

func (msg *Message) TopicIs(t, st uint8) bool {
	return msg.Topic == t && msg.SubTopic == st
}

func (msg *Message) TopicToString() string {
	return fmt.Sprintf("(%d,%d)", msg.Topic, msg.SubTopic)
}

// 消息封包
func Pack(topic, subTopic uint8, body []byte) []byte {
	msg := NewMessage(topic, subTopic)
	msg.Body = body
	return msg.Encode()
}

// 消息解包
func Unpack(buf []byte) (msg *Message, err error) {
	msg = NewMessage(0, 0)
	err = msg.Decode(buf)
	return msg, err
}

// 拆包检查func
func PackSlitFunc(data []byte, atEOF bool) (advance int, token []byte, err error) {
	// 检查是否结束，数据长度是否足够，消息版本是否对应。
	if !atEOF && len(data) > MESSAGE_HEAD_SIZE && data[0] == MESSAGE_VERSION {
		// 读出数据包实际数据长度
		length := binary.LittleEndian.Uint32(data[3:7])
		if int(length) <= len(data) {
			return int(length), data[:int(length)], nil
		}
	}
	return
}
