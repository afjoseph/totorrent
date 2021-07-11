package peer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/fileinfo"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/util"
)

var ERR_PEER_LISTEN = errors.New("ERR_PEER_LISTEN")
var ERR_PEER_LISTEN_EOF = errors.New("ERR_PEER_LISTEN_EOF")
var ERR_PEER_LISTEN_TIMEOUT = errors.New("ERR_PEER_LISTEN_TIMEOUT")
var ERR_PEER_LISTEN_RESET = errors.New("ERR_PEER_LISTEN_RESET")
var ERR_PEER_HANDSHAKE = errors.New("ERR_PEER_HANDSHAKE")
var ERR_SEND_INTERESTED = errors.New("ERR_SEND_INTERESTED")
var ERR_PARSE_PEER = errors.New("ERR_PARSE_PEER")
var ERR_PEERMSG_FUNC = errors.New("ERR_PEERMSG_FUNC")
var ERR_REQUESTPEERMSG = errors.New("ERR_REQUESTPEERMSG")
var ERR_BITFIELDPEERMSG = errors.New("ERR_BITFIELDPEERMSG")
var ERR_PEERMSG_BUILD = errors.New("ERR_PEERMSG_BUILD")

const (
	PEERMSG_HANDSHAKE      = 99
	PEERMSG_KEEP_ALIVE     = 98
	PEERMSG_CHOKED         = 0
	PEERMSG_UNCHOKED       = 1
	PEERMSG_INTERESTED     = 2
	PEERMSG_NOT_INTERESTED = 3
	PEERMSG_HAVE           = 4
	PEERMSG_BITFIELD       = 5
	PEERMSG_REQUEST        = 6
	PEERMSG_PIECE          = 7
	MAX_PEERMSG_TYPES      = 10
)

type PeerMsg interface {
	String() string
	Bytes() ([]byte, error)
	GetPayload() []byte
	SetPayload([]byte)
}

type HavePeerMsg struct {
	Payload []byte
}

func (self *HavePeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *HavePeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func BytesOrPanic(peerMsg PeerMsg) []byte {
	buff, err := peerMsg.Bytes()
	if err != nil {
		panic(err)
	}
	return buff
}

func (self *HavePeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	payloadReader := bytes.NewReader(self.Payload)
	buff.Write([]byte{
		// Length = 0x04 (pieceIdx length) + 0x01 (type)
		0x00, 0x00, 0x00, 0x05,
		// type
		PEERMSG_HAVE,
	})
	// piece idx
	pieceIdxBuff := make([]byte, 4)
	_, err := payloadReader.Read(pieceIdxBuff)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	buff.Write(pieceIdxBuff)
	return buff.Bytes(), nil
}

func (self *HavePeerMsg) String() string {
	return fmt.Sprintf("HavePeerMsg{Payload: %v}", self.Payload)
}

func (self *HavePeerMsg) GetPieceIdx() int {
	return int(binary.BigEndian.Uint32(self.Payload))
}

type RequestPeerMsg struct {
	Payload []byte
}

func (self *RequestPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *RequestPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *RequestPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	payloadReader := bytes.NewReader(self.Payload)
	buff.Write([]byte{
		// Length
		0x00, 0x00, 0x00, 0x0d,
		// Type
		PEERMSG_REQUEST,
	})
	// block info
	blockInfoBuff := make([]byte, 12)
	_, err := payloadReader.Read(blockInfoBuff)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	buff.Write(blockInfoBuff)
	return buff.Bytes(), nil
}

func (self *RequestPeerMsg) String() string {
	return fmt.Sprintf("RequestPeerMsg{Payload: %v}", self.Payload)
}

func (self *RequestPeerMsg) GetPieceIdx() (int, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, 4)
	_, err := reader.ReadAt(buff, 0)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return int(binary.BigEndian.Uint32(buff)), nil
}

func (self *RequestPeerMsg) GetBlockBegin() (uint32, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, 4)
	_, err := reader.ReadAt(buff, 4)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return binary.BigEndian.Uint32(buff), nil
}

func (self *RequestPeerMsg) GetBlockLength() (uint32, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, 4)
	_, err := reader.ReadAt(buff, 8)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return binary.BigEndian.Uint32(buff), nil
}

// GetBlock() basically makes a new Block out of the info we have in
// RequestPeerMsg
func (self *RequestPeerMsg) GetBlock() (*piece.Block, error) {
	blockBegin, err := self.GetBlockBegin()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	blockLength, err := self.GetBlockLength()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	pieceIdx, err := self.GetPieceIdx()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	return piece.NewBlock(int(blockBegin/blockLength), pieceIdx, blockBegin, blockLength, nil), nil
}

type HandshakePeerMsg struct {
	Payload []byte
}

func (self *HandshakePeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *HandshakePeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *HandshakePeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write(self.Payload)
	return buff.Bytes(), nil
}

func (self *HandshakePeerMsg) String() string {
	return fmt.Sprintf("HandshakePeerMsg{Payload: %v}", self.Payload)
}

func (self *HandshakePeerMsg) GetInfoHash() ([20]byte, error) {
	reader := bytes.NewReader(self.Payload)
	var buff [20]byte
	_, err := reader.ReadAt(buff[:], 1+19+8)
	if err != nil {
		return [20]byte{}, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return buff, nil
}

func (self *HandshakePeerMsg) GetPeerId() ([20]byte, error) {
	reader := bytes.NewReader(self.Payload)
	var buff [20]byte
	_, err := reader.ReadAt(buff[:], 1+19+8+20)
	if err != nil {
		return [20]byte{}, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return buff, nil
}

type ChokedPeerMsg struct {
	Payload []byte
}

func (self *ChokedPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *ChokedPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *ChokedPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write([]byte{
		// Length
		0x00, 0x00, 0x00, 0x01,
		// type
		PEERMSG_CHOKED,
	})
	return buff.Bytes(), nil
}

func (self *ChokedPeerMsg) String() string {
	return "ChokedPeerMsg"
}

type UnchokedPeerMsg struct {
	Payload []byte
}

func (self *UnchokedPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *UnchokedPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *UnchokedPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write([]byte{
		// Length
		0x00, 0x00, 0x00, 0x01,
		// type
		PEERMSG_UNCHOKED,
	})
	return buff.Bytes(), nil
}

func (self *UnchokedPeerMsg) String() string {
	return "UnchokedPeerMsg"
}

type PiecePeerMsg struct {
	Payload []byte
}

func (self *PiecePeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *PiecePeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *PiecePeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	payloadReader := bytes.NewReader(self.Payload)
	// Length
	lengthBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuff,
		uint32(len(self.Payload)+1)) // Plus one byte for Type
	buff.Write(lengthBuff)
	// Type
	buff.Write([]byte{PEERMSG_PIECE})
	// Block buff
	blockBuff := make([]byte, len(self.Payload))
	_, err := payloadReader.Read(blockBuff)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	buff.Write(blockBuff)
	return buff.Bytes(), nil
}

func (self *PiecePeerMsg) String() string {
	return fmt.Sprintf("PiecePeerMsg{len(Payload): %d}", len(self.Payload))
}

func (self *PiecePeerMsg) GetPieceIdx() (int, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, 4)
	_, err := reader.ReadAt(buff, 0)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return int(binary.BigEndian.Uint32(buff)), nil
}

func (self *PiecePeerMsg) GetBlockBegin() (uint32, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, 4)
	_, err := reader.ReadAt(buff, 4)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return binary.BigEndian.Uint32(buff), nil
}

func (self *PiecePeerMsg) GetBlockBuff() ([]byte, error) {
	reader := bytes.NewReader(self.Payload)
	buff := make([]byte, len(self.Payload)-8)
	_, err := reader.ReadAt(buff, 8)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEERMSG_FUNC, err.Error())
	}
	return buff, nil
}

// GetBlock() basically makes a new Block out of the info we have in
// RequestPeerMsg
func (self *PiecePeerMsg) GetBlock() (*piece.Block, error) {
	blockBegin, err := self.GetBlockBegin()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	blockBuff, err := self.GetBlockBuff()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	pieceIdx, err := self.GetPieceIdx()
	if err != nil {
		return nil, print.ErrorWrapf(ERR_REQUESTPEERMSG, err.Error())
	}
	return piece.NewBlock(int(blockBegin)/len(blockBuff),
		pieceIdx, blockBegin,
		uint32(len(blockBuff)), blockBuff), nil
}

type BitfieldPeerMsg struct {
	Payload []byte
}

func (self *BitfieldPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *BitfieldPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *BitfieldPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	// length = 0x01 (type) + payload length (AKA: bitfield length)
	lengthBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuff, uint32(1+len(self.Payload)))
	buff.Write(lengthBuff)
	// Type
	buff.Write([]byte{PEERMSG_BITFIELD})
	// payload
	buff.Write(self.Payload)
	return buff.Bytes(), nil
}

func (self *BitfieldPeerMsg) String() string {
	return fmt.Sprintf("BitfieldPeerMsg{Payload: %v}", self.Payload)
}

func (self *BitfieldPeerMsg) GetPieceIndices(maxPieceIdx int) ([]int, error) {
	field := util.NewBitfieldFromBytes(self.Payload)
	if maxPieceIdx > field.NumOfBits {
		return nil, print.ErrorWrapf(
			ERR_PEERMSG_BUILD,
			"maxPieceIdx > field.NumOfBits: %d > %d",
			maxPieceIdx, field.NumOfBits)
	}
	return field.AsIntSlice(maxPieceIdx), nil
}

type InterestedPeerMsg struct {
	Payload []byte
}

func (self *InterestedPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *InterestedPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *InterestedPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write([]byte{
		// Length
		0x00, 0x00, 0x00, 0x01,
		// type
		PEERMSG_INTERESTED,
	})
	return buff.Bytes(), nil
}

func (self *InterestedPeerMsg) String() string {
	return "InterestedPeerMsg"
}

type NotInterestedPeerMsg struct {
	Payload []byte
}

func (self *NotInterestedPeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *NotInterestedPeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *NotInterestedPeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write([]byte{
		// Length
		0x00, 0x00, 0x00, 0x01,
		// type
		PEERMSG_NOT_INTERESTED,
	})
	return buff.Bytes(), nil
}

func (self *NotInterestedPeerMsg) String() string {
	return "NotInterestedPeerMsg"
}

type KeepAlivePeerMsg struct {
	Payload []byte
}

func (self *KeepAlivePeerMsg) GetPayload() []byte {
	return self.Payload
}

func (self *KeepAlivePeerMsg) SetPayload(b []byte) {
	self.Payload = b
}

func (self *KeepAlivePeerMsg) Bytes() ([]byte, error) {
	var buff bytes.Buffer
	buff.Write([]byte{0x00, 0x00, 0x00, 0x00})
	return buff.Bytes(), nil
}

func (self *KeepAlivePeerMsg) String() string {
	return "KeepAlivePeerMsg"
}

// NewPeerMsgFromBuffer parses 'msgBuf' to make a PeerMsg from the expected messages we can make
func NewPeerMsgFromBuffer(msgBuf []byte) (PeerMsg, error) {
	if IsPeerHandshakeMsg(msgBuf) {
		return BuildPeerHandshakeMsgFromRawBytes(msgBuf)
	}

	// All messages, apart from the handshake message, follow this format:
	//
	// 	  <length prefix><message ID><payload>
	//
	// Where the 'length prefix' is always a big-endian uint32 and 'message ID'
	// is always one byte
	var msgIdBuff []byte = nil
	if len(msgBuf) > 4 {
		msgIdBuff = make([]byte, 1)
		_, err := bytes.NewReader(msgBuf).ReadAt(msgIdBuff, 4)
		if err != nil {
			return nil, err
		}
	}
	// If message is bigger than 5, we have a payload. Put everything else as
	// the payload
	var payloadBuff []byte = nil
	if len(msgBuf) > 5 {
		payloadBuff = msgBuf[5:]
	}

	var msg PeerMsg
	switch {
	case msgIdBuff == nil:
		// we have no ID, then this is a KEEP_ALIVE message
		msg = &KeepAlivePeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_INTERESTED:
		msg = &InterestedPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_NOT_INTERESTED:
		msg = &NotInterestedPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_UNCHOKED:
		msg = &UnchokedPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_CHOKED:
		msg = &ChokedPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_HAVE:
		msg = &HavePeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_BITFIELD:
		msg = &BitfieldPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_REQUEST:
		msg = &RequestPeerMsg{}
		msg.SetPayload(payloadBuff)
	case msgIdBuff[0] == PEERMSG_PIECE:
		msg = &PiecePeerMsg{}
		msg.SetPayload(payloadBuff)
	default:
		return nil, print.ErrorWrapf(ERR_PARSE_PEER,
			"Failed to parse peer msg: %v", msgBuf)
	}
	return msg, nil
}

func BuildPeerHandshakeMsgFromRawBytes(msgBuf []byte) (PeerMsg, error) {
	if msgBuf == nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, "nil pointer")
	}
	reader := bytes.NewReader(msgBuf)
	peerId := make([]byte, 20)
	_, err := reader.ReadAt(peerId, 48)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	infoHash := make([]byte, 20)
	_, err = reader.ReadAt(infoHash, 28)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	var fixedSizeInfoHash [20]byte
	copy(fixedSizeInfoHash[:], infoHash)

	var fixedSizePeerId [20]byte
	copy(fixedSizePeerId[:], peerId)
	return BuildPeerHandshakeMsg(fixedSizePeerId, fixedSizeInfoHash), nil
}

func BuildPeerHandshakeMsg(peerId [20]byte, infohash [20]byte) PeerMsg {
	print.DebugFunc()
	peerMsg := &HandshakePeerMsg{}

	// Length
	var payloadBuff bytes.Buffer
	payloadBuff.WriteByte(19)
	// Protocol name
	payloadBuff.WriteString("BitTorrent protocol")
	// Reserved bytes
	payloadBuff.Write([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	// Info hash
	payloadBuff.Write(infohash[:])
	// Peer id
	payloadBuff.Write(peerId[:])

	peerMsg.Payload = payloadBuff.Bytes()
	return peerMsg
}

func BuildPeerPieceMsg(block *piece.Block) PeerMsg {
	print.DebugFunc()
	peerMsg := &PiecePeerMsg{}
	var payloadBuff bytes.Buffer

	// pieceIdx
	pieceIdxBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(pieceIdxBuff, uint32(block.PieceIdx))
	payloadBuff.Write(pieceIdxBuff)
	// offset
	offsetBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(offsetBuff, block.Begin)
	payloadBuff.Write(offsetBuff)
	// buff
	payloadBuff.Write(block.Buff)

	peerMsg.Payload = payloadBuff.Bytes()
	return peerMsg
}

// BuildPeerBitfieldMsg will:
// * Make a 'bitfield' struct of size = fileInfo.PieceLength[0].PieceLength
// * For each piece, check if it has been downloaded
//   * Do this by having a function func `(self Piece) isDownloaded()` that
//     loops through all blocks and see their status
// * Check the bitfield[pieceIdx] == 0 or 1 based on that
// * Serialize the bitfield struct to an actual bitfield
// * Store it in Payload
func BuildPeerBitfieldMsg(pieces piece.SafePieces) (PeerMsg, error) {
	print.DebugFunc()
	if pieces == nil || pieces.Len() == 0 {
		return nil, print.ErrorWrapf(ERR_PEERMSG_BUILD,
			"BuildPeerBitfieldMsg: fileinfo has nil pieces")
	}

	// Construct the bitfield from pieces
	peerMsg := &BitfieldPeerMsg{}
	var payloadBuff bytes.Buffer
	bitfield := util.NewBitfield(pieces.MaxPieceIdx())
	for _, p := range pieces.Data() {
		if p.Downloaded() {
			bitfield.Set(p.Idx())
		}
	}
	payloadBuff.Write(bitfield.Bytes)
	peerMsg.Payload = payloadBuff.Bytes()
	return peerMsg, nil
}

func BuildPeerHaveMsg(pieceIdx uint32) PeerMsg {
	print.DebugFunc()
	peerMsg := &HavePeerMsg{}
	var payloadBuff bytes.Buffer

	// pieceIdx
	pieceIdxBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(pieceIdxBuff, pieceIdx)
	payloadBuff.Write(pieceIdxBuff)

	peerMsg.Payload = payloadBuff.Bytes()
	return peerMsg
}

func BuildPeerInterestedMsg() PeerMsg {
	print.DebugFunc()
	return &InterestedPeerMsg{}
}

func BuildPeerNotInterestedMsg() PeerMsg {
	print.DebugFunc()
	return &NotInterestedPeerMsg{}
}

func BuildPeerKeepAliveMsg() PeerMsg {
	print.DebugFunc()
	return &KeepAlivePeerMsg{}
}

func BuildPeerUnchokedMsg() PeerMsg {
	print.DebugFunc()
	return &UnchokedPeerMsg{}
}

func BuildPeerChokedMsg() PeerMsg {
	print.DebugFunc()
	return &ChokedPeerMsg{}
}

func IsPeerHandshakeMsg(msgBuf []byte) bool {
	// print.DebugFunc()
	if msgBuf == nil {
		return false
	}
	// Check message length
	reader := bytes.NewReader(msgBuf)
	messageLength, err := reader.ReadByte()
	if err != nil {
		return false
	}
	if 68 != int(messageLength)+49 {
		return false
	}

	// check protocol info
	fixedProtocolMsg := make([]byte, 19)
	_, err = reader.ReadAt(fixedProtocolMsg, 1)
	if err != nil {
		return false
	}
	if string(fixedProtocolMsg) != "BitTorrent protocol" {
		return false
	}

	return true
}

func ParseRawPeerMsgLength(msgBuf []byte) (int, error) {
	if msgBuf == nil {
		return 0, print.ErrorWrapf(ERR_PEER_LISTEN, "Nil buff")
	}
	// XXX A peer's handshake msg length is actually stored in the first byte,
	// as opposed to all other peer messages, which are stored as a big-endian
	// uint32
	if IsPeerHandshakeMsg(msgBuf) {
		return 68, nil
	}
	// Read length as a big-endian uint32
	msgLenBuff := make([]byte, 4)
	reader := bytes.NewReader(msgBuf)
	_, err := reader.ReadAt(msgLenBuff, 0)
	if err != nil {
		return 0, print.ErrorWrapf(ERR_PEER_LISTEN,
			"Couldn't read 4 bytes peer msg buff (buff: [%v] | err: [%s])", msgBuf, err.Error())
	}
	msgLen := int(binary.BigEndian.Uint32(msgLenBuff)) + 4 // Plus 4 bytes for the length itself
	// XXX See "View #2" under the explanation for a 'request' peer msg here:
	// https://wiki.theory.org/BitTorrentSpecification#request:_.3Clen.3D0013.3E.3Cid.3D6.3E.3Cindex.3E.3Cbegin.3E.3Clength.3E
	// It is typically agreed that the largest message will be 2^14 (16384), or 256*64.
	// So, i've increased this to 256*65=16640, just to make sure we don't mess
	// some important metadata
	if msgLen > 16640 {
		return 0, print.ErrorWrapf(ERR_PEER_LISTEN,
			"peer msg is too big: will not process")
	}
	return msgLen, nil
}

func BuildPeerRequestMsg(targetBlock *piece.Block) (PeerMsg, error) {
	print.DebugFunc()

	if targetBlock == nil {
		return nil, print.ErrorWrapf(ERR_PEERMSG_BUILD,
			"BuildPeerRequestMsg: block is nil")
	}

	peerMsg := &RequestPeerMsg{}
	var payloadBuff bytes.Buffer
	pieceIdxBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(pieceIdxBuff, uint32(targetBlock.PieceIdx))
	payloadBuff.Write(pieceIdxBuff)
	beginBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(beginBuff, targetBlock.Begin)
	payloadBuff.Write(beginBuff)
	lengthBuff := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuff, targetBlock.Length)
	payloadBuff.Write(lengthBuff)

	peerMsg.Payload = payloadBuff.Bytes()
	return peerMsg, nil
}

// BuildPeerHandshakeMsg(
// 	[20]byte{0x00, 0x11}, // peerID: content doesn't matter in this test
// 	// since we're just checking the msg, not the content
// 	sha1.Sum([]byte{0x00, 0x11, 0x22, 0x33, 0x44})),
// BuildPeerHaveMsg(1),
// BuildPeerPieceMsg(piece.NewBlock(1, 0, 4, 3, []byte{0x11, 0x22, 0x33})),
// BuildPeerInterestedMsg(),
// BuildPeerUnchokedMsg(),
// })

// GetRandomPeerMsgs is a utility function to be run in tests: you can panic here
func GetRandomPeerMsgs(count int) []PeerMsg {
	peerMsgs := []PeerMsg{}
	for i := 0; i < count; i++ {
		var msg PeerMsg
		var err error
		switch rand.Intn(MAX_PEERMSG_TYPES - 1) {
		case PEERMSG_HANDSHAKE:
			var buff [20]byte
			rand.Read(buff[:])
			msg = BuildPeerHandshakeMsg(buff, buff)
		case PEERMSG_KEEP_ALIVE:
			msg = BuildPeerKeepAliveMsg()
		case PEERMSG_CHOKED:
			msg = BuildPeerChokedMsg()
		case PEERMSG_UNCHOKED:
			msg = BuildPeerUnchokedMsg()
		case PEERMSG_INTERESTED:
			msg = BuildPeerInterestedMsg()
		case PEERMSG_NOT_INTERESTED:
			msg = BuildPeerNotInterestedMsg()
		case PEERMSG_HAVE:
			msg = BuildPeerHaveMsg(uint32(rand.Int()))
		case PEERMSG_BITFIELD:
			fileInfo, err := fileinfo.NewFileInfo(
				util.RandRange(1, 100), int64(util.RandRange(1, 100)),
				util.RandRange(1, 100), "", "", nil,
				fileinfo.DefaultFinalizeFunc, false)
			if err != nil {
				break
			}
			msg, err = BuildPeerBitfieldMsg(fileInfo.Pieces)
			if err != nil {
				break
			}
		case PEERMSG_REQUEST:
			var buff [40]byte
			rand.Read(buff[:])
			msg, err = BuildPeerRequestMsg(piece.NewBlock(rand.Intn(10),
				rand.Intn(10),
				uint32(rand.Intn(10)), uint32(rand.Intn(10)), buff[:]))
		case PEERMSG_PIECE:
			var buff [40]byte
			rand.Read(buff[:])
			msg = BuildPeerPieceMsg(piece.NewBlock(rand.Intn(10),
				rand.Intn(10),
				uint32(rand.Intn(10)), uint32(rand.Intn(10)), buff[:]))
		default:
			msg = BuildPeerKeepAliveMsg()
		}
		if err != nil {
			panic(err)
		}
		if msg == nil {
			panic("nil msg")
		}
		peerMsgs = append(peerMsgs, msg)
	}
	return peerMsgs
}
