package peer

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/afjoseph/commongo/print"
	commongoutil "github.com/afjoseph/commongo/util"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/fileinfo"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/projectpath"
	"github.com/afjoseph/totorrent/util"
	"github.com/stretchr/testify/require"
)

// Test_ConnectWithAllPeers_Handshake_Choked does the following:
// * Initializes a client peer
// * Initializes a bunch of test peers
// * Connects the client peer with the test peers
// * Client peer will send HANDSHAKE messages to all test peers
// * Test peers will respond with a HANDSHAKE message of their own
// * Client peer will send an INTERESTED message as a response to each received
//   HANDSHAKE msg
// * All test peers will respond with a CHOKED message to short-circuit the
//   communication
// * Client peer will end the connection immediately
//
// Through this process, we should have a bunch of handshake messages sent back
// and forth. In a regular scenario, this should start the whole download
// routine, but we're having the test peers respond with a CHOKED (when the
// client sends an INTERESTED msg), just to minimize the actions of this test.
//
// XXX This test's template is the most wholistic functional test: all other
// functional tests should adapt to this
func Test_ConnectWithAllPeers_Handshake_Choked(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// --------
	// - Init Metainfo
	myMetainfo := metainfo.MetaInfo{
		InfoBytes: []byte{0x00, 0x11, 0x22, 0x33, 0x44}, // Content doesn't matter
	}
	// - Init a random FileInfo: doesn't matter in this test
	fileInfo, err := fileinfo.NewFileInfoFilledWithRandomData(
		5, 5, 5, "", "", nil, fileinfo.DefaultFinalizeFunc, false)
	require.NoError(t, err)
	// - Init testPeers
	// Their behaviour will be:
	// * Send HANDSHAKE as a response to a HANDSHAKE msg, with a random delay
	// * Send CHOKED as a response to an INTERESTED msg
	testPeers := []*Peer{}
	testPeerErrChan := make(chan PeerAndError, 10)
	for i := 0; i < 8; i++ {
		testPeers = append(testPeers, NewTestPeer(
			i, fileInfo,
			nil, // OnConnectFunc
			// PeerMsgFunc
			func(peerConn *PeerConnection, peerMsg PeerMsg, errChan chan error) {
				if _, ok := peerMsg.(*HandshakePeerMsg); ok {
					print.Debugf(
						"TEST: Received HANDSHAKE from client. Sending back HANDSHAKE...\n")
					time.Sleep(time.Duration(util.RandRange(1, 3)) * time.Second)
					peerConn.WritePeerMsg(
						BuildPeerHandshakeMsg(peerConn.FromPeer.ID,
							sha1.Sum(myMetainfo.InfoBytes)))
				} else if _, ok := peerMsg.(*InterestedPeerMsg); ok {
					// Answer with a CHOKED to short-circuit the handshake process
					print.Debugf("TEST: Received INTERESTED from client. Sending back CHOKED\n")
					time.Sleep(time.Duration(util.RandRange(1, 3)) * time.Second)
					peerConn.WritePeerMsg(BuildPeerChokedMsg())
				} else {
					require.Failf(t,
						"", "TEST: Observed unknown peerMsg from client: %+v", peerMsg)
				}
			},
			nil,
		))
		// Start the listener
		err := testPeers[i].ListenForPeers(&myMetainfo, testPeerErrChan)
		require.NoError(t, err)
		defer testPeers[i].Close()
	}
	// - Init clientPeer
	// It's behaviour will be:
	// * Send a HANDSHAKE message upon connecting with a peer
	//     * Done with DefaultSendHandshakeFunc
	// * Record all the HANDSHAKE msgs we get
	clientPeer, err := NewClientPeer(
		fileInfo, // doesn't matter
		nil,      // OnConnectFunc
		DefaultPeerMsgFunc,
		DefaultSendHandshakeFunc,
		true, // doTrackMsgs
	)
	defer require.NoError(t, clientPeer.Close())
	require.NoError(t, err)

	// Action
	// --------
	// XXX - This is a BLOCKING call
	require.NoError(t,
		clientPeer.ConnectWithAllPeers(
			&myMetainfo,
			testPeers, 0,
			func(targetPeer *Peer, err error) {
				targetPeer.Close()
				require.Failf(t, "", "Peer [%s] failed with err: %w", targetPeer, err)
			},
		),
	)

	// Assertions
	// --------
	// - Implicitly, there's an assertion that nothing fails (AKA: no errors
	// are received)
	// - Also, check if we get HANDSHAKE and CHOKED msgs as responses from
	// testPeers
	for _, testPeer := range testPeers {
		// Get all HANDSHAKE msgs we got from this peer
		var inboundHandshakeMsgs []*HandshakePeerMsg
		for _, msg := range filterPeerMsgsOfType(clientPeer.inboundPeerMsgs, &HandshakePeerMsg{}) {
			if typedMsg, ok := msg.(*HandshakePeerMsg); ok {
				peerId, err := typedMsg.GetPeerId()
				require.NoError(t, err)
				if bytes.Equal(peerId[:], testPeer.ID[:]) {
					inboundHandshakeMsgs = append(inboundHandshakeMsgs, typedMsg)
				}
			}
		}
		// Make sure we have one of each
		require.Equal(t, len(inboundHandshakeMsgs), 1)
		// Check if TestPeer sent proper Choked msgs
		require.Equal(t,
			len(filterPeerMsgsOfType(testPeer.outboundPeerMsgs, &ChokedPeerMsg{})),
			1)
	}
	// Get all CHOKED msgs we got from all peers
	require.Equal(t,
		len(filterPeerMsgsOfType(clientPeer.inboundPeerMsgs, &ChokedPeerMsg{})),
		len(testPeers))
	// -- Also, check testPeers error channel
	select {
	case peerAndError := <-testPeerErrChan:
		switch {
		case errors.Is(peerAndError.Error, ERR_ACCEPT_CONN__CLOSED_CONN):
			// Ignore: this just means we forcefully closed the connection
		default:
			require.Failf(t,
				"Received error from testPeer [%s]: %v",
				peerAndError.Peer.StringifyID(), peerAndError.Error)
		}
	default:
		// No error was received: do nothing
	}
}

// Test_RequestNextPiece does the following:
// * Initializes a client peer
// * Initializes a bunch of test peers
// * Client peer dials the test peers
// * Setup each peerConn.piecesToDownloadQueue to have all pieces
//   * This basically means we've received a lot of HAVE/BITFIELD requests
func Test_RequestNextPiece(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// --------
	// - Make a "filled" FileInfo, that TestPeers can use
	//   - This assumes that the TestPeers have all the pieces and are
	//     "seeding" now
	completedFileInfo, err := fileinfo.NewFileInfoFilledWithRandomData(
		30, // totalPieces
		8,  // pieceLength
		4,  // blockLength
		util.MkdirTempOrPanic("", "totorrentfinaltmp_"),
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		nil,
		func(*fileinfo.FileInfo) error { return nil },
		true, // isSeeding
	)
	require.NoError(t, err)
	defer completedFileInfo.DeleteBlocksDir()
	defer completedFileInfo.DeleteFinalDownloadDir()
	// - Make an "empty" FileInfo, that clientPeer will use
	//   - This is just an empty FileInfo that clientPeer will use
	//   - This really means that clientPeer read the torrent file and is now
	//     ready to download the pieces
	clientFileInfo, err := fileinfo.NewFileInfo(
		30, // totalPieces
		8,  // pieceLength
		4,  // blockLength
		util.MkdirTempOrPanic("", "totorrentfinaltmp_"),
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		nil,
		func(*fileinfo.FileInfo) error { return nil },
		false,
	)
	require.NoError(t, err)
	defer clientFileInfo.DeleteBlocksDir()
	// - Init clientPeer
	clientPeer, err := NewClientPeer(
		clientFileInfo,
		nil, // OnConnectFunc
		// PeerMsgFunc as normal
		DefaultPeerMsgFunc,
		// No SendHandshakeFunc since we won't send handshakes
		nil,
		// doTrackMsgs
		true,
	)
	defer clientPeer.Close()
	require.NoError(t, err)
	// - Init testPeers
	peerErrChan := make(chan PeerAndError, 10)
	errChan := make(chan error, 10)
	fullPieceDownloadedChan := make(chan int, clientFileInfo.Pieces.Len())
	for i := 0; i < 9; i++ {
		i2 := i
		testPeer := NewTestPeer(
			i,
			completedFileInfo,
			// OnConnectFunc
			nil,
			// PeerMsgFunc
			// - Just respond to a REQUEST msg with a PIECE msg using a block
			//   from 'completedFileInfo'
			func(peerConn *PeerConnection, peerMsg PeerMsg, errChan chan error) {
				if requestPeerMsg, ok := peerMsg.(*RequestPeerMsg); ok {
					// time.Sleep(time.Duration(util.RandRange(1, 3)) * time.Second)
					b, err := requestPeerMsg.GetBlock()
					require.NoError(t, err)
					print.Debugf(
						"TestPeer [%d]: Received REQUEST from clientPeer. Sending PIECE msg %v...\n",
						i2, b)
					fullBlock, err := peerConn.FromPeer.FileInfo.GetBlock(
						b.PieceIdx,
						b.Begin, b.Length, false)
					require.NoError(t, err)
					require.NotNil(t, fullBlock)
					go func() {
						peerConn.WritePeerMsg(BuildPeerPieceMsg(fullBlock))
					}()
				} else {
					require.Fail(t,
						"TEST: Observed unknown peerMsg from client: %+v", peerMsg)
				}
			},
			// No SendHandshakeFunc since we won't send handshakes
			nil,
		)
		// - Have testPeer listen for connections from new peers
		err := testPeer.ListenForPeers(nil, peerErrChan)
		require.NoError(t, err)
		defer testPeer.Close()
		// - Have clientPeer dial the newly-created testPeer
		peerConn, err := DialPeer(clientPeer, testPeer, peerErrChan)
		require.NoError(t, err)
		// - Fill testPeer's piecesToDownloadQueue with all the piece indices
		//   - This just means that we've received multiple HAVE/BITFIELD msgs
		//     from this testPeer that tell us this testPeer is 'seeding' and has
		//     ALL the pieces.
		// XXX There's a situation we're not testing: if a torrent has X
		// seeding peers, but there exists Y pieces that NO peer has; when that
		// happens, this program will just exit, but this is a VALID situation
		// we are not testing and not properly accounting for.
		peerConn.piecesToDownloadQueue = make([]int, clientFileInfo.Pieces.Len())
		copy(peerConn.piecesToDownloadQueue, clientFileInfo.Pieces.AsIntSlice())
		// - For each peerConn, start listening for msgs on the wire
		go peerConn.listenForMessages(peerErrChan)
		defer peerConn.Conn.Close()

		// Action
		// --------
		pieceIdx := peerConn.piecesToDownloadQueue[0]
		peerConn.blockRequester = NewBlockRequester(peerConn,
			peerConn.FromPeer.FileInfo.Pieces.Get(pieceIdx).BlockCount(),
			DefaultWasBlockReceived,
			fullPieceDownloadedChan,
		)
		peerConn.RequestNextPiece(clientFileInfo.Pieces, errChan)
	}

	// Assertions
	// --------
	// - Implicitly, there's an assertion that nothing fails (AKA: no errors
	//   are received)
	// - Assert that errChan is clean
	//   - ignore connection closed down errors, since a PeerConn that had its
	//     sister PeerConn (PeerConn connected to the opposite Peer) Conn
	//     object closed will yield this error
	// - Assert that we received a number of pieces equal to expected number of
	//   pieces
	receivedPieceIndices := []int{}
	{
		// XXX This block does the following:
		// - Check if we received an error
		//   - If so, fail the test
		// - Check if we received a pieceIdx on the downloaded piece channel
		//   - If we did, rewind the timeout mechanism
		//   - and loop again
		// - Retry the timeout mechanism 3 times
		//   - If it is exceeded, we'll exit this block to check if we received
		//     all the pieces we need to receive
		sleepDurationPerRetry := 7 * time.Second
		maxRetries := 3
		retryCounter := maxRetries
	looper:
		select {
		case peerAndError := <-peerErrChan:
			switch {
			case errors.Is(peerAndError.Error, ERR_ACCEPT_CONN__CLOSED_CONN):
				// Ignore: this just means we forcefully closed the connection
			default:
				require.Failf(t,
					"",
					"Received error from testPeer [%s]: %v",
					peerAndError.Peer, peerAndError.Error)
			}
		case err := <-errChan:
			require.Failf(t,
				"",
				"Received error from testPeer [%s]: %v",
				err.Error())
		case downloadedPieceIdx := <-fullPieceDownloadedChan:
			// Record the piece
			receivedPieceIndices = append(receivedPieceIndices, downloadedPieceIdx)
			// There are still pieces being downloaded: rewind the timeout mechanism
			retryCounter = maxRetries
			// and loop again
			goto looper
		default:
			print.Debugf("Decrementing assertion loop counter [%d/%d]...\n", retryCounter, maxRetries)
			retryCounter--
			if retryCounter > 0 {
				time.Sleep(sleepDurationPerRetry)
				goto looper
			}
		}
	}
	// - Assert that we received all possible pieces
	require.ElementsMatch(t, clientFileInfo.Pieces.AsIntSlice(), receivedPieceIndices)
}

func Test_filterPeerMsgsOfType(t *testing.T) {
	for _, tc := range []struct {
		title                         string
		inputPeerMsgs                 []PeerMsg
		inputPeerMsgType              PeerMsg
		expectedFilterPeerMsgsIndices []int
	}{
		{
			title: "Good #1",
			inputPeerMsgs: []PeerMsg{
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerChokedMsg(),
				BuildPeerChokedMsg(),
				BuildPeerUnchokedMsg(),
				BuildPeerUnchokedMsg(),
			},
			inputPeerMsgType:              &ChokedPeerMsg{},
			expectedFilterPeerMsgsIndices: []int{2, 3},
		},
		{
			title: "Good #2",
			inputPeerMsgs: []PeerMsg{
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerChokedMsg(),
				BuildPeerChokedMsg(),
				BuildPeerUnchokedMsg(),
				BuildPeerUnchokedMsg(),
			},
			inputPeerMsgType:              &UnchokedPeerMsg{},
			expectedFilterPeerMsgsIndices: []int{4, 5},
		},
		{
			title: "Good #3",
			inputPeerMsgs: []PeerMsg{
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerChokedMsg(),
				BuildPeerChokedMsg(),
				BuildPeerUnchokedMsg(),
				BuildPeerUnchokedMsg(),
			},
			inputPeerMsgType:              &HandshakePeerMsg{},
			expectedFilterPeerMsgsIndices: []int{0, 1},
		},
		{
			title:                         "Empty inputPeerMsgs",
			inputPeerMsgs:                 []PeerMsg{},
			inputPeerMsgType:              &HandshakePeerMsg{},
			expectedFilterPeerMsgsIndices: nil,
		},
		{
			title:                         "Nil inputPeerMsgs",
			inputPeerMsgs:                 []PeerMsg{},
			inputPeerMsgType:              &HandshakePeerMsg{},
			expectedFilterPeerMsgsIndices: nil,
		},
		{
			title: "Nil inputPeerMsgType",
			inputPeerMsgs: []PeerMsg{
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerHandshakeMsg([20]byte{0xaa}, [20]byte{0xaa}),
				BuildPeerChokedMsg(),
				BuildPeerChokedMsg(),
				BuildPeerUnchokedMsg(),
				BuildPeerUnchokedMsg(),
			},
			inputPeerMsgType:              nil,
			expectedFilterPeerMsgsIndices: nil,
		},
	} {
		t.Run(tc.title, func(t *testing.T) {
			// Preparation: Extract expectedFilteredPeerMsgs from tc.expectedFilterPeerMsgsIndices
			var expectedFilteredPeerMsgs []PeerMsg
			if tc.expectedFilterPeerMsgsIndices == nil {
				expectedFilteredPeerMsgs = nil
			} else {
				expectedFilteredPeerMsgs = []PeerMsg{}
				for _, i := range tc.expectedFilterPeerMsgsIndices {
					expectedFilteredPeerMsgs = append(expectedFilteredPeerMsgs, tc.inputPeerMsgs[i])
				}
			}
			// Action
			actualFilteredPeerMsgs := filterPeerMsgsOfType(tc.inputPeerMsgs, tc.inputPeerMsgType)
			// Assert
			require.Equal(t, expectedFilteredPeerMsgs, actualFilteredPeerMsgs)
		})
	}
}

// See 78d762_21_06_30_Wed_13_27
// TODO: Fix this test
// func Test_ConnectWithAllPeers(t *testing.T) {
// 	print.SetLevel(print.LOG_DEBUG)

// 	for _, tc := range []struct {
// 		title                string
// 		inputTorrentFilepath string
// 	}{
// 		{
// 			title:                "Good",
// 			inputTorrentFilepath: filepath.Join(projectpath.Root, "small_1.torrent"),
// 		},
// 	} {

// 		// Setup
// 		// -----
// 		// - Extract fileInfo from torrent file
// 		// We use metainfo only for getting the sha1 of the handshake
// 		myMetainfo, err := util.DecodeTorrentFile(tc.inputTorrentFilepath)
// 		require.NoError(t, err)
// 		// We should use myInfo for comparing hash of each piece
// 		myInfo, err := myMetainfo.UnmarshalInfo()
// 		require.NoError(t, err)
// 		// - Make an "empty" FileInfo, that clientPeer will use
// 		//   - This is just an empty FileInfo that clientPeer will use
// 		//   - This really means that clientPeer read the torrent file and is now
// 		//     ready to download the pieces
// 		clientFileInfo, err := fileinfo.NewFileInfo(
// 			len(myInfo.Pieces)/20,
// 			myInfo.PieceLength,
// 			piece.BLOCK_LENGTH,
// 			"", "",
// 			nil,
// 			false,
// 		)
// 		require.NoError(t, err)
// 		defer clientFileInfo.DeleteBlocksDir()
// 		// - Make a "filled" FileInfo, that TestPeers can use
// 		//   - This assumes that all testPeers have all the pieces of this
// 		//   torrent and are "seeding" now
// 		completedFileInfo, err := fileinfo.NewFileInfoFilledWithRandomData(
// 			len(myInfo.Pieces)/20,
// 			myInfo.PieceLength,
// 			piece.BLOCK_LENGTH,
// 			true, // isSeeding
// 		)
// 		require.NoError(t, err)
// 		defer completedFileInfo.DeleteBlocksDir()
// 		// - Make a new clientPeer
// 		clientPeer, err := NewClientPeer(
// 			clientFileInfo, nil,
// 			DefaultPeerMsgFunc,
// 			DefaultSendHandshakeFunc,
// 			true,
// 		)
// 		require.NoError(t, err)
// 		defer clientPeer.Close()
// 		// - Init testPeers
// 		peerErrChan := make(chan PeerAndError, 10)
// 		// TODO When you increase this to 6+, random "sending on closed
// 		// connection" issues happen
// 		testPeers := []*Peer{}
// 		for i := 0; i < 6; i++ {
// 			testPeer := NewTestPeer(
// 				i,
// 				completedFileInfo,
// 				nil, // OnConnectFunc
// 				DefaultPeerMsgFunc,
// 				DefaultSendHandshakeFunc,
// 			)
// 			// - Have testPeer listen for connections from new peers
// 			err := testPeer.ListenForPeers(myMetainfo, peerErrChan)
// 			require.NoError(t, err)
// 			defer testPeer.Close()
// 			testPeers = append(testPeers, testPeer)
// 		}

// 		// Action
// 		// --------
// 		// XXX - This is a BLOCKING call
// 		require.NoError(t,
// 			clientPeer.ConnectWithAllPeers(
// 				myMetainfo,
// 				testPeers,
// 				func(targetPeer *Peer, err error) {
// 					require.Failf(t, "Peer [%s] failed with err: %v", targetPeer.String(), err)
// 				},
// 			),
// 		)

// 		// Assertions
// 		// ---------
// 		// - Implicitly, there's an assertion that nothing fails (AKA: no errors
// 		//   are received)
// 		// - Assert that peerErrChan is clean
// 		select {
// 		case peerAndError := <-peerErrChan:
// 			switch {
// 			case errors.Is(peerAndError.Error, ERR_ACCEPT_CONN__CLOSED_CONN):
// 				// Ignore: this just means we forcefully closed the connection
// 			default:
// 				require.Failf(t,
// 					"Received error from testPeer [%s]: %v",
// 					peerAndError.Peer.String(), peerAndError.Error)
// 			}
// 		}
// 	}
// }

func Test_ConnectWithAllPeers_Download(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// --------
	// - Decode and unmarshal the torrent file
	inputTorrentFilepath := filepath.Join(projectpath.Root,
		"test_assets", "random_bytes__1.torrent")
	inputFinalDownloadDirPath := filepath.Join(projectpath.Root,
		"test_assets", "random_bytes__1")
	inputFinalDownloadFilepath := filepath.Join(
		inputFinalDownloadDirPath, "random_bytes__1")
	torrentMetainfo, err := util.DecodeTorrentFile(inputTorrentFilepath)
	require.NoError(t, err)
	torrentInfo, err := torrentMetainfo.UnmarshalInfo()
	require.NoError(t, err)
	// - Make a new torrent file out of the info
	completedFileInfo, err := fileinfo.NewFileInfo(
		// We always divide by 20 (length of a sha1 hash) when parsing torrent
		// files
		len(torrentInfo.Pieces)/20,
		torrentInfo.PieceLength,
		piece.BLOCK_LENGTH,
		inputFinalDownloadDirPath,
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		&torrentInfo,
		fileinfo.DefaultFinalizeFunc,
		true,
	)
	require.NoError(t, err)
	// - Make a new FileInfo, that clientPeer will use:
	//   - This is just an empty FileInfo
	//   - This means that clientPeer has successfully read the torrent file
	//     and is now ready to download pieces from it
	clientFileInfo, err := fileinfo.NewFileInfo(
		len(torrentInfo.Pieces)/20,
		torrentInfo.PieceLength,
		piece.BLOCK_LENGTH,
		util.MkdirTempOrPanic("", "totorrenttesttmp_"),
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		&torrentInfo,
		fileinfo.DefaultFinalizeFunc,
		false,
	)
	require.NoError(t, err)
	defer clientFileInfo.DeleteBlocksDir()
	defer clientFileInfo.DeleteFinalDownloadDir()
	// - Init clientPeer
	clientPeer, err := NewClientPeer(
		clientFileInfo,
		nil,
		DefaultPeerMsgFunc,
		DefaultSendHandshakeFunc,
		false,
	)
	defer clientPeer.Close()
	require.NoError(t, err)
	// - Init testPeers
	peerErrChan := make(chan PeerAndError, 10)
	testPeers := []*Peer{}
	for i := 0; i < 8; i++ {
		testPeer := NewTestPeer(
			i, completedFileInfo,
			nil,
			DefaultPeerMsgFunc,
			DefaultSendHandshakeFunc,
		)
		// - Have testPeer listen for connections from new peers
		err := testPeer.ListenForPeers(torrentMetainfo, peerErrChan)
		require.NoError(t, err)
		defer testPeer.Close()
		testPeers = append(testPeers, testPeer)
	}

	// Action
	// --------
	// XXX - This is a BLOCKING call
	require.NoError(t,
		clientPeer.ConnectWithAllPeers(
			torrentMetainfo,
			testPeers, 0,
			func(targetPeer *Peer, err error) {
				targetPeer.Close()
				require.Fail(t, "Peer [%s] failed with err: %w", targetPeer, err)
			},
		),
	)

	// Assertions
	// --------
	// - Implicitly, there's an assertion that nothing fails (AKA: no errors
	//   are received)
	// - Assert that the file clientPeer downloaded is the same as the one
	// testPeers are seeding from
	dirEntry, err := os.ReadDir(clientPeer.FileInfo.FinalDownloadDir)
	require.NoError(t, err)
	require.Equal(t, 1, len(dirEntry))
	for _, file := range dirEntry {
		fp := filepath.Join(clientPeer.FileInfo.FinalDownloadDir, file.Name())
		_, _, actualExitCode, err := commongoutil.Exec("", "cmp -s %s %s",
			inputFinalDownloadFilepath, fp)
		require.NoError(t, err)
		require.Equal(t, 0, actualExitCode)
	}
}
