package peer

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/projectpath"
	"github.com/afjoseph/totorrent/util"
	"github.com/stretchr/testify/require"
)

type PeerMsgRepo struct {
	mutex   sync.Mutex
	msgs    []PeerMsg
	currIdx int
}

func NewPeerMsgRepo(msgs []PeerMsg) *PeerMsgRepo {
	return &PeerMsgRepo{msgs: msgs, currIdx: 0}
}

func (self *PeerMsgRepo) Done() bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.currIdx >= len(self.msgs)
}

func (self *PeerMsgRepo) Next() (PeerMsg, bool) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.currIdx >= len(self.msgs) {
		return nil, false
	}
	msg := self.msgs[self.currIdx]
	self.currIdx++
	return msg, true
}

// Test_ReadPeerMsg runs ReadPeerMsg() on an active PeerConnection between a test peer.
// The test peer is an "echo" peer: it just sends back all PeerMsgs upon accepting a connection
// XXX Test_ReadPeerMsg covers NewPeerMsgFromBuffer() as well
func Test_ReadPeerMsg(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// ------------
	// - Setup expected msgs
	peerMsgRepo := NewPeerMsgRepo(GetRandomPeerMsgs(200))
	// - Setup a TestPeer
	testPeerErrChan := make(chan PeerAndError, 10)
	errChan := make(chan error, 10)
	peerMsgChan := make(chan PeerMsg, 10)
	for i := 0; i < 10; i++ {
		testPeer := NewTestPeer(
			i,
			nil,
			// OnConnectFunc
			func(peerConn *PeerConnection) error {
				fmt.Printf("TestPeer [%v]: OnConnect\n", peerConn.FromPeer)
				for {
					peerMsg, ok := peerMsgRepo.Next()
					if !ok {
						return nil
					}
					peerConn.WritePeerMsg(peerMsg)
				}
			},
			nil, nil,
		)
		// - Have testPeer listen for connections from new peers
		err := testPeer.ListenForPeers(nil, testPeerErrChan)
		require.NoError(t, err)
		defer testPeer.Close()
		// - Have clientPeer dial the newly-created testPeer
		peerConn, err := DialPeer(nil, testPeer, testPeerErrChan)
		require.NoError(t, err)

		// Action
		// ------
		// - For each peerConn, read the msg and send it over to peerMsgChan
		go peerConn.StartPeerMsgReaderLoop(peerMsgChan, errChan)
		go func() {
			for {
				peerMsg, err := peerConn.ReadPeerMsg()
				if err != nil {
					errChan <- err
					return
				}
				peerMsgChan <- peerMsg
			}
		}()
	}
	// - Block and read all msgs from peerMsgChan until peerMsgRepo runs out
	actualPeerMsgs := []PeerMsg{}
looper:
	select {
	case peerMsg := <-peerMsgChan:
		actualPeerMsgs = append(actualPeerMsgs, peerMsg)
		goto looper
	case <-time.After(2 * time.Second):
		// Do nothing
	}

	// Assert
	// -------
	// - Assert that our testPeer had no errors
	select {
	case peerAndError := <-testPeerErrChan:
		switch {
		case errors.Is(peerAndError.Error, ERR_PEERCONN_WRITE__CLOSED_CONN):
			// Ignore: this just means we forcefully closed the connection
		default:
			require.Failf(t,
				"",
				"Received error from testPeer [%s]: %v",
				peerAndError.Peer, peerAndError.Error)
		}
	default:
	}
	// - Assert send PeerMsgs are received
	require.ElementsMatch(t, peerMsgRepo.msgs, actualPeerMsgs)
}

func Test_IsPeerHandshakeMsg(t *testing.T) {
	for _, tc := range []struct {
		title          string
		inputBuff      []byte
		expectedOutput bool
	}{
		{
			title: "proper handshake",
			inputBuff: []byte{
				// Length: 0x13 == 19
				0x13,
				// Name: BitTorrent protocol
				0x42, 0x69, 0x74, 0x54, 0x6f, 0x72, 0x72, 0x65, 0x6e,
				0x74, 0x20, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				// 20 bytes: sha1 hash of info dictionary
				0x2f, 0x5a, 0x5c, 0xcb, 0x7d, 0xc3, 0x2b, 0x7f, 0x7d, 0x7b,
				0x15, 0x0d, 0xd6, 0xef, 0xbc, 0xe8, 0x7d, 0x2f, 0xc3, 0x71,
				// 20 bytes: peer ID
				0x2d, 0x42, 0x54, 0x37, 0x61, 0x35, 0x57, 0x2d, 0x8f, 0xb3,
				0x31, 0xfe, 0x21, 0x09, 0x7b, 0xa5, 0x19, 0x3a, 0x22, 0xb1,
			},
			expectedOutput: true,
		},
		{
			// This should work just fine since we're just checking the first
			// two parts of a handshake message
			title: "half a handshake",
			inputBuff: []byte{
				// Length: 0x13 == 19
				0x13,
				// Name: BitTorrent protocol
				0x42, 0x69, 0x74, 0x54, 0x6f, 0x72, 0x72, 0x65, 0x6e,
				0x74, 0x20, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
			expectedOutput: true,
		},
		{
			title: "bad length",
			inputBuff: []byte{
				// Length: 0xff == 255
				// XXX This should always be 0x13
				0xff,
				// Name: BitTorrent protocol
				0x42, 0x69, 0x74, 0x54, 0x6f, 0x72, 0x72, 0x65, 0x6e,
				0x74, 0x20, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				// 20 bytes: sha1 hash of info dictionary
				0x2f, 0x5a, 0x5c, 0xcb, 0x7d, 0xc3, 0x2b, 0x7f, 0x7d, 0x7b,
				0x15, 0x0d, 0xd6, 0xef, 0xbc, 0xe8, 0x7d, 0x2f, 0xc3, 0x71,
				// 20 bytes: peer ID
				0x2d, 0x42, 0x54, 0x37, 0x61, 0x35, 0x57, 0x2d, 0x8f, 0xb3,
				0x31, 0xfe, 0x21, 0x09, 0x7b, 0xa5, 0x19, 0x3a, 0x22, 0xb1,
			},
			expectedOutput: false,
		},
		{
			title: "Bad protocol name",
			inputBuff: []byte{
				// Length: 0x13 == 19
				0x13,
				// Name: whatever
				// XXX Name must always be "BitTorrent protocol" for this to be
				// considered a handshake
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				// 20 bytes: sha1 hash of info dictionary
				0x2f, 0x5a, 0x5c, 0xcb, 0x7d, 0xc3, 0x2b, 0x7f, 0x7d, 0x7b,
				0x15, 0x0d, 0xd6, 0xef, 0xbc, 0xe8, 0x7d, 0x2f, 0xc3, 0x71,
				// 20 bytes: peer ID
				0x2d, 0x42, 0x54, 0x37, 0x61, 0x35, 0x57, 0x2d, 0x8f, 0xb3,
				0x31, 0xfe, 0x21, 0x09, 0x7b, 0xa5, 0x19, 0x3a, 0x22, 0xb1,
			},
			expectedOutput: false,
		},
		{
			title:          "Null buff",
			inputBuff:      nil,
			expectedOutput: false,
		},
		{
			title:          "Empty buff",
			inputBuff:      []byte{},
			expectedOutput: false,
		},
	} {
		t.Run(tc.title, func(t *testing.T) {
			actualOutput := IsPeerHandshakeMsg(tc.inputBuff)
			require.Equal(t, tc.expectedOutput, actualOutput)
		})
	}
}

func Test_ParseRawPeerMsgLength(t *testing.T) {
	for _, tc := range []struct {
		title             string
		inputBuff         []byte
		expectedMsgLength int
		expectedErr       error
	}{
		{
			title: "Handshake: Proper handshake",
			inputBuff: BytesOrPanic(BuildPeerHandshakeMsg(
				[20]byte{0x2d, 0x42, 0x54, 0x37, 0x61, 0x35, 0x57, 0x2d, 0x8f, 0xb3,
					0x31, 0xfe, 0x21, 0x09, 0x7b, 0xa5, 0x19, 0x3a, 0x22, 0xb1},
				[20]byte{0x2f, 0x5a, 0x5c, 0xcb, 0x7d, 0xc3, 0x2b, 0x7f, 0x7d, 0x7b,
					0x15, 0x0d, 0xd6, 0xef, 0xbc, 0xe8, 0x7d, 0x2f, 0xc3, 0x71},
			)),
			expectedMsgLength: 68,
		},
		{
			// XXX This should work just fine since we're just checking the first
			// two parts of a handshake message
			title: "Handshake: half a handshake",
			inputBuff: []byte{
				// Length: 0x13 == 19
				0x13,
				// Name: BitTorrent protocol
				0x42, 0x69, 0x74, 0x54, 0x6f, 0x72, 0x72, 0x65, 0x6e,
				0x74, 0x20, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
			expectedMsgLength: 68,
		},
		{
			title: "Handshake: bad handshake length",
			inputBuff: []byte{
				// Length: 0xff == 255
				0xff,
				// Name: BitTorrent protocol
				0x42, 0x69, 0x74, 0x54, 0x6f, 0x72, 0x72, 0x65, 0x6e,
				0x74, 0x20, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
				// 8 bytes: Reserved bytes
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				// 20 bytes: sha1 hash of info dictionary
				0x2f, 0x5a, 0x5c, 0xcb, 0x7d, 0xc3, 0x2b, 0x7f, 0x7d, 0x7b,
				0x15, 0x0d, 0xd6, 0xef, 0xbc, 0xe8, 0x7d, 0x2f, 0xc3, 0x71,
				// 20 bytes: peer ID
				0x2d, 0x42, 0x54, 0x37, 0x61, 0x35, 0x57, 0x2d, 0x8f, 0xb3,
				0x31, 0xfe, 0x21, 0x09, 0x7b, 0xa5, 0x19, 0x3a, 0x22, 0xb1,
			},
			// XXX Since this is not a handshake, we'll process the first 4
			// bytes to get the length. This means the length of this msg is
			// technically `(binary.BigEndian.Uint32([]byte(0x42, 0x69, 0x74, 0x54)))`,
			// which is much greater than our constant 16640 max message length
			expectedMsgLength: 0,
			expectedErr:       ERR_PEER_LISTEN,
		},
		{
			title:             "INTERESTED",
			inputBuff:         BytesOrPanic(BuildPeerInterestedMsg()),
			expectedMsgLength: 5,
		},
		{
			title: "PIECE",
			inputBuff: BytesOrPanic(BuildPeerPieceMsg(
				piece.NewBlockFromFile(0x1f9, 0x00,
					filepath.Join(projectpath.Root, "test_assets/piece_1")),
			)),
			expectedMsgLength: 16397,
		},
		{
			title: "Short buff",
			// Must be at least 4 bytes
			inputBuff:         []byte{0x00, 0x00, 0x11},
			expectedMsgLength: 0,
			expectedErr:       ERR_PEER_LISTEN,
		},
		{
			title:             "Null buff",
			inputBuff:         nil,
			expectedMsgLength: 0,
			expectedErr:       ERR_PEER_LISTEN,
		},
		{
			title:             "Empty buff",
			inputBuff:         nil,
			expectedMsgLength: 0,
			expectedErr:       ERR_PEER_LISTEN,
		},
	} {
		t.Run(tc.title, func(t *testing.T) {
			actualOutput, actualErr := ParseRawPeerMsgLength(tc.inputBuff)
			require.ErrorIs(t, actualErr, tc.expectedErr)
			require.Equal(t, tc.expectedMsgLength, actualOutput)
		})
	}
}

func Test_bitfield_msg_assembly(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	for i := 0; i < 100; i++ {
		// Make a new pieces
		inputPieces := piece.NewSafePieces()
		// Fill it with random pieces
		for i := 0; i < 100; i++ {
			inputPieces.Put(piece.NewPiece(
				util.RandRange(1, 100),
				int64(util.RandRange(1, 100)),
				piece.PIECESTATUS_DONE,
			))
		}
		// Make a bitfield msg out of it
		msg, err := BuildPeerBitfieldMsg(inputPieces)
		require.NoError(t, err)
		// Get the bitfield back from the msg
		field := util.NewBitfieldFromBytes(msg.GetPayload())
		// Assert that the fields match
		require.ElementsMatch(t,
			inputPieces.AsIntSlice(),
			field.AsIntSlice(inputPieces.MaxPieceIdx()))
	}
}
