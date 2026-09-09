// Package client implements the real TCP lifecycle used by phase-one bots.
package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"google.golang.org/protobuf/proto"
)

const (
	magic       = 0x4E52
	version     = 1
	headerSize  = 16
	maxBodySize = 64 * 1024
)

// Run connects one bot, logs in, matches, sends 30Hz movement, and drains
// authoritative snapshots until ctx ends.
func Run(ctx context.Context, address string, clientID int) error {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	var frameSequence uint32
	send := func(messageType pb.MessageType, message proto.Message) error {
		frameSequence++
		return sendMessage(conn, messageType, frameSequence, message)
	}

	if err := send(pb.MessageType_MSG_LOGIN_REQUEST, &pb.LoginRequest{
		ProtocolVersion: 1,
		Token:           "dev",
		DisplayName:     fmt.Sprintf("bot-%d", clientID),
	}); err != nil {
		return err
	}
	var login pb.LoginResponse
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_LOGIN_RESPONSE, &login); err != nil {
		return err
	}
	if login.Reason != pb.ReasonCode_REASON_OK {
		return fmt.Errorf("login rejected: %s", login.Reason)
	}
	if err := send(pb.MessageType_MSG_MATCH_REQUEST, &pb.MatchRequest{}); err != nil {
		return err
	}
	var found pb.MatchFound
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_MATCH_FOUND, &found); err != nil {
		return err
	}
	if found.RoomId == 0 {
		return errors.New("match returned zero room id")
	}
	_ = conn.SetReadDeadline(time.Time{})

	inputTicker := time.NewTicker(time.Second / 30)
	defer inputTicker.Stop()
	readErrors := make(chan error, 1)
	var snapshots atomic.Uint64
	var acknowledged atomic.Bool
	go func() {
		for {
			messageType, body, err := readFrame(reader)
			if err != nil {
				readErrors <- err
				return
			}
			if messageType == pb.MessageType_MSG_DISCONNECT {
				var disconnect pb.Disconnect
				_ = proto.Unmarshal(body, &disconnect)
				readErrors <- fmt.Errorf("server disconnected: %s (%s)", disconnect.Reason, disconnect.Message)
				return
			}
			if messageType == pb.MessageType_MSG_WORLD_SNAPSHOT {
				var snapshot pb.WorldSnapshot
				if err := proto.Unmarshal(body, &snapshot); err != nil || snapshot.Self == nil {
					readErrors <- errors.New("invalid authoritative snapshot")
					return
				}
				snapshots.Add(1)
				if snapshot.LastProcessedInput > 0 {
					acknowledged.Store(true)
				}
			}
		}
	}()
	var inputSequence uint32
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			<-readErrors
			if snapshots.Load() == 0 || !acknowledged.Load() {
				return errors.New("bot received no acknowledged world snapshot")
			}
			return ctx.Err()
		case err := <-readErrors:
			return err
		case now := <-inputTicker.C:
			inputSequence++
			direction := float32(1)
			if clientID%2 != 0 {
				direction = -1
			}
			if err := send(pb.MessageType_MSG_PLAYER_INPUT, &pb.PlayerInput{
				InputSeq:     inputSequence,
				ClientTickMs: uint64(now.UnixMilli()),
				Move:         &pb.Vec2{X: direction},
			}); err != nil {
				return err
			}
		}
	}
}

func sendMessage(conn net.Conn, messageType pb.MessageType, sequence uint32, message proto.Message) error {
	body, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	if len(body) > maxBodySize {
		return errors.New("message exceeds body limit")
	}
	frame := make([]byte, headerSize+len(body))
	binary.BigEndian.PutUint16(frame[0:2], magic)
	frame[2] = version
	binary.BigEndian.PutUint16(frame[4:6], uint16(messageType))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(body)))
	binary.BigEndian.PutUint32(frame[12:16], sequence)
	copy(frame[headerSize:], body)
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(frame)
	return err
}

func readUntil(ctx context.Context, conn net.Conn, reader *bufio.Reader, want pb.MessageType, out proto.Message) error {
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetReadDeadline(deadline)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		messageType, body, err := readFrame(reader)
		if err != nil {
			return err
		}
		if messageType == pb.MessageType_MSG_DISCONNECT {
			var disconnect pb.Disconnect
			_ = proto.Unmarshal(body, &disconnect)
			return fmt.Errorf("server disconnected: %s (%s)", disconnect.Reason, disconnect.Message)
		}
		if messageType == want {
			return proto.Unmarshal(body, out)
		}
	}
}

func readFrame(reader *bufio.Reader) (pb.MessageType, []byte, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	if binary.BigEndian.Uint16(header[0:2]) != magic || header[2] != version || header[3] != 0 ||
		binary.BigEndian.Uint16(header[6:8]) != 0 {
		return 0, nil, errors.New("invalid frame header")
	}
	length := binary.BigEndian.Uint32(header[8:12])
	if length > maxBodySize {
		return 0, nil, errors.New("frame body exceeds limit")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return pb.MessageType(binary.BigEndian.Uint16(header[4:6])), body, nil
}
