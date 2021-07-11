package peer

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/afjoseph/commongo/print"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/util"
)

const ReadTimeoutDuration = 20 * time.Second

var ERR_ACCEPT_CONN = errors.New("ERR_ACCEPT_CONN")
var ERR_ACCEPT_CONN__CLOSED_CONN = errors.New("ERR_ACCEPT_CONN__CLOSED_CONN")
var ERR_SEND_HANDSHAKE = errors.New("ERR_SEND_HANDSHAKE")
var ERR_CONNECT_PEERS = errors.New("ERR_CONNECT_PEERS")
var ERR_DIAL_PEER = errors.New("ERR_DIAL_PEER")
var ERR_LISTEN_FOR_PEERS = errors.New("ERR_LISTEN_FOR_PEERS")
var ERR_PEERCONN_WRITE = errors.New("ERR_PEERCONN_WRITE")
var ERR_PEERCONN_WRITE__CLOSED_CONN = errors.New("ERR_PEERCONN_WRITE__CLOSED_CONN")

type PeerConnection struct {
	Conn                  net.Conn
	ToPeer                *Peer
	FromPeer              *Peer
	piecesToDownloadQueue []int
	misfires              uint
	blockRequester        *BlockRequester
	connWriterChan        chan PeerMsg
}

func NewPeerConnection(
	fromPeer, toPeer *Peer,
	errChan chan PeerAndError) *PeerConnection {
	peerConn := &PeerConnection{
		FromPeer:       fromPeer,
		ToPeer:         toPeer,
		connWriterChan: make(chan PeerMsg, 1000),
	}

	// Infinite loop to receive a PeerMsg and sequentially write it. This is
	// crucial so that messages arrive intact
	go func() {
		for {
			peerMsg, ok := <-peerConn.connWriterChan
			if !ok {
				return
			}
			print.Debugf("Writing peerMsg [%+v] on [%v]\n",
				peerMsg, peerConn)

			start := time.Now()
			b, err := peerMsg.Bytes()
			if err != nil {
				errChan <- PeerAndError{peerConn.FromPeer,
					print.ErrorWrapf(ERR_PEERCONN_WRITE, err.Error())}
				return
			}
			n, err := peerConn.Conn.Write(b)
			if err != nil {
				switch {
				case strings.Contains(err.Error(), "broken pipe"):
					errChan <- PeerAndError{peerConn.FromPeer,
						print.ErrorWrapf(ERR_PEERCONN_WRITE__CLOSED_CONN, err.Error())}
				default:
					errChan <- PeerAndError{peerConn.FromPeer,
						print.ErrorWrapf(ERR_PEERCONN_WRITE, err.Error())}
				}
				return
			}
			if n != len(b) {
				panic("SHIT")
			}
			print.Debugf("[duration: %s] Written peerMsg [%+v] on [%v]\n",
				time.Since(start), peerMsg, peerConn)
			// TODO This function is run asynchronously and we might get a race
			// condition here
			if peerConn.FromPeer != nil && peerConn.FromPeer.doTrackMsgs {
				peerConn.FromPeer.outboundPeerMsgs = append(
					peerConn.FromPeer.outboundPeerMsgs, peerMsg)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	return peerConn
}

func (self *PeerConnection) String() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("PeerConnection{Conn: [%s -> %s] | ",
		self.Conn.LocalAddr(), self.Conn.RemoteAddr()))
	if self.FromPeer != nil {
		sb.WriteString(fmt.Sprintf("FromPeer: [%+v] | ", self.FromPeer))
	}
	if self.ToPeer != nil {
		sb.WriteString(fmt.Sprintf("ToPeer: [%+v]}", self.ToPeer))
	}

	return sb.String()
}

func (self *PeerConnection) ReadPeerMsg() (PeerMsg, error) {
	// print.Debugf("START: Reading msg on %v...\n", self)

	totalBuff := []byte{}
	msgBuf := make([]byte, 1024)
	remainingMsgLen := 0
reader:
	// print.Debugf("LOOP: Reading msg on %v...\n", self)
	// Read the from socket
	err := self.Conn.SetReadDeadline(time.Now().Add(ReadTimeoutDuration))
	if err != nil {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
	}
	start := time.Now()
	c, err := self.Conn.Read(msgBuf)
	msgReadDuration := time.Since(start)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, print.ErrorWrapf(ERR_PEER_LISTEN_EOF, err.Error())
		} else if err, ok := err.(net.Error); ok && err.Timeout() {
			if self.FromPeer != nil && self.FromPeer.IsDownloading() {
				print.Debugf("Still downloading; ignoring read timeout on [%v]\n", self)
				time.Sleep(100 * time.Millisecond)
				goto reader
			}
			return nil, print.ErrorWrapf(ERR_PEER_LISTEN_TIMEOUT, err.Error())
		} else if strings.Contains(err.Error(), "connection reset by peer") {
			return nil, print.ErrorWrapf(ERR_PEER_LISTEN_RESET, err.Error())
		} else {
			return nil, print.ErrorWrapf(ERR_PEER_LISTEN, err.Error())
		}
	}
	if c == 0 {
		return nil, print.ErrorWrapf(ERR_PEER_LISTEN, "Received no data and no error")
	}
	// If we've never read the msg length before, do it now
	// Else, skip over it
	if remainingMsgLen == 0 {
		remainingMsgLen, err = ParseRawPeerMsgLength(msgBuf)
		if err != nil {
			return nil, err
		}
	}
	print.Debugf("[duration: %s]: Msg read (length:%d) on %v\n",
		msgReadDuration, remainingMsgLen, self)
	// Three cases: We wanna parse a 250-byte msg in a 100-byte buffer
	// - first msg in a packet is sent
	//   - This is expected to be followed by more messages
	//   - For example, payload for 250 on a buff with 100.
	//     c=100
	//     len(msgBuf)=100
	//	   remainingMsgLen=250
	//	   We can take the first
	//	     msgBuf[:min(len(msgBuf), remainingMsgLen)]
	// - 2nd msg arrives
	//     c=100
	//	   len(msgBuf)=100
	//	   remainingMsgLen=150
	//	   We take the 2nd 100
	//	   msgBuf[:min(len(msgBuf), remainingMsgLen)]
	//	- 3rd and last msg arrives, but it is coupled with another msg
	//	   c=80 (50 from the last packet and 30 from another packet)
	//	   len(msgBuf)=100
	//	   remainingMsgLen=50
	//	   we wanna take only 50
	//	     msgBuf[:c] is not gonna work
	//	     msgBuf[:min(len(msgBuf), remainingMsgLen)]
	//  - Technically there is a fourth case: if the remainingMsgLen <
	//    len(msgBuf) in the first place: our
	//    msgBuf[:min(len(msgBuf),remainingMsgLen)] will work with it too.
	readLength := util.Min(len(msgBuf), remainingMsgLen)
	remainingMsgLen -= readLength
	totalBuff = append(totalBuff, msgBuf[:readLength]...)
	if remainingMsgLen > 0 {
		print.Debugf("We need to read some more. Looping back...\n")
		goto reader
	}
	peerMsg, err := NewPeerMsgFromBuffer(totalBuff)
	if err != nil {
		return nil, err
	}
	// Append this msg to self.FromPeer.inboundPeerMsgs
	// TODO This function is run asynchronously and we might get a race
	// condition here
	if self.FromPeer != nil && self.FromPeer.doTrackMsgs {
		self.FromPeer.inboundPeerMsgs = append(self.FromPeer.inboundPeerMsgs, peerMsg)
	}

	return peerMsg, nil
}

func (self *PeerConnection) StartPeerMsgReaderLoop(
	peerMsgChan chan PeerMsg, errChan chan error) {
	for {
		// Read messages forever
		peerMsg, err := self.ReadPeerMsg()
		if err != nil {
			errChan <- err
			return
		}
		print.Debugf("SENDER peerMsg: %v\n", peerMsg)
		peerMsgChan <- peerMsg
	}
}

// listenForMessages starts an infinite loop to read all messages (using the
// socket connection 'conn') from 'targetPeer'. If any error occurs, send it
// through errChan
// TODO Rework the SENDER and RECEIVER msg loops: you don't need both
func (self *PeerConnection) listenForMessages(parentErrChan chan PeerAndError) {
	print.DebugFunc()

	// XXX Technically, we should keep reading these messages forever. That
	// being said, there's a read timeout.
	errChan := make(chan error)
	peerMsgChan := make(chan PeerMsg, 100)
	go self.StartPeerMsgReaderLoop(peerMsgChan, errChan)
	// Do something with the msg
	if self.FromPeer.PeerMsgFunc != nil {
		go func() {
			// Enter an infinite loop where we receive a msg, process it in
			// another routine, and listen for more msgs
			for {
				print.Debugf("RECEIVED peerMsg\n")
				peerMsg, ok := <-peerMsgChan
				if !ok {
					return
				}
				go self.FromPeer.PeerMsgFunc(self, peerMsg, errChan)
			}
		}()
	}

	// Block and handle all errors
	// XXX We should keep reading messages until something closes the net.Conn
	// object, which will yield an error and we'll reach here
	// TODO Some errors don't require a complete shutdown: we should consider
	// making this a for-loop and handling those errors gracefully so as not to
	// kill the connection
	err := <-errChan
	switch {
	// XXX We're intentionally ignoring this error
	case errors.Is(err, ERR_PEER_LISTEN_EOF):
		print.Debugf("Found EOF with PeerConnection [%s]. Killing connection...\n",
			self.String())
	case errors.Is(err, ERR_PEER_LISTEN_RESET):
		print.Debugf("Connection reset with PeerConnection [%s]. Killing connection...\n",
			self.String())
	// XXX We're intentionally ignoring this error
	case errors.Is(err, ERR_PEER_LISTEN_TIMEOUT):
		print.Debugf("Read timeout with PeerConnection [%s]. Killing connection...\n",
			self.String())
	case strings.Contains(err.Error(), "use of closed network connection"):
		print.Debugf("Listening on closed connection [%s]. Killing connection...\n",
			self.String())
	case err != nil:
		print.Warnf("Found unknown error in PeerConnection [%s]: [%v]\n",
			self.String(), err)
		parentErrChan <- PeerAndError{self.FromPeer, err}
	}
}

// XXX This function is BLOCKING
func acceptConn(
	listeningPeer *Peer,
	listener net.Listener,
	errChan chan PeerAndError) (*PeerConnection, error) {
	peerConn := NewPeerConnection(listeningPeer, nil, errChan)

	// Accept connections
	conn, err := listener.Accept()
	if err != nil {
		if strings.Contains(err.Error(), "use of closed network connection") {
			return peerConn, print.ErrorWrapf(ERR_ACCEPT_CONN__CLOSED_CONN, err.Error())
		}
	}
	peerConn.Conn = conn
	// Make a PeerConnection out of it
	toIp, toPort := util.GetIpAndPortFromNetAddr(conn.RemoteAddr())
	peerConn.ToPeer = &Peer{
		IP:   toIp,
		Port: toPort,
	}

	return peerConn, nil
}

func DialPeer(
	fromPeer *Peer,
	targetPeer *Peer,
	errChan chan PeerAndError) (*PeerConnection, error) {
	print.Debugf("Dialing peer [%v]\n", targetPeer)
	peerConn := NewPeerConnection(fromPeer, targetPeer, errChan)

	// Check Ipv4
	if targetPeer.IP.To4() == nil {
		return peerConn, print.ErrorWrapf(
			ERR_DIAL_PEER,
			"Peer [%+v] uses IPv6, which we don't support now\n", targetPeer)
	}
	// Establish connection
	dialer := net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.Dial("tcp4", fmt.Sprintf("%s:%s",
		targetPeer.IP.String(), strconv.Itoa(targetPeer.Port)))
	if err != nil {
		return peerConn, print.ErrorWrapf(
			ERR_DIAL_PEER, err.Error())
	}
	peerConn.Conn = conn
	print.Debugf("Connection with peer %v established: %s -> %s\n",
		targetPeer, conn.LocalAddr(), conn.RemoteAddr())

	return peerConn, nil
}

func (self *PeerConnection) WritePeerMsg(peerMsg PeerMsg) {
	self.connWriterChan <- peerMsg
}

type ErrorWithID struct {
	ID    string
	Error error
}

type PeerAndError struct {
	Peer  *Peer
	Error error
}

// connectWithPeer will ASYNCHRONOUSLY establish a TCP connection with 'peer'
// and do two things:
// - Send them a handshake message
// - Listen for messages
// Later, we'll need to figure out how to download the file itself
func (self *Peer) connectWithPeer(
	myMetainfo *metainfo.MetaInfo,
	targetPeer *Peer,
	parentDoneChan chan *Peer,
	parentErrChan chan PeerAndError,
) {
	print.DebugFunc()

	// Dial Peer
	peerConn, err := DialPeer(self, targetPeer, parentErrChan)
	if err != nil {
		print.Warnln(err.Error())
		parentErrChan <- PeerAndError{targetPeer, err}
		return
	}
	self.PeerConnections = append(self.PeerConnections, peerConn)
	// Send peer handshake, if we can
	// XXX Handshake is always sent first by the "From" client, not the "To"
	// client. The "To" client will respond to it with a handshake of their own
	if self.SendHandshakeFunc != nil {
		go func() {
			err := self.SendHandshakeFunc(peerConn, myMetainfo)
			if err != nil {
				print.Warnf(
					"Killing connection to peer [%v] because of an error while sending HANDSHAKE [%v]\n",
					peerConn.ToPeer, err)
				peerConn.Conn.Close()
				parentErrChan <- PeerAndError{peerConn.ToPeer, err}
				return
			}
		}()
	}
	// Start listener
	// XXX This is a BLOCKING call
	peerConn.listenForMessages(parentErrChan)
	// peerConn.Conn.Close()
	parentDoneChan <- peerConn.ToPeer
}

// RequestNextPiece dequeues self.piecesToDownloadQueue and tries to
// concurrently reserve and download it. It'll move on to the next piece if it
// is already reserved.
// "Downloading" a piece means it'll call 'requestBlockFunc' (AKA send a
// REQUEST peer msg) for all blocks in that piece, without waiting for the
// result.
func (self *PeerConnection) RequestNextPiece(
	availablePeerPieces piece.SafePieces,
	errChan chan error,
) int {
	print.Debugf("Requesting next piece on [%v]\n", self)

looper:
	if len(self.piecesToDownloadQueue) == 0 {
		print.Debugf("No available pieces left on [%v]\n", self)
		self.FromPeer.MarkAsNotDownloading()
		self.ToPeer.MarkAsNotDownloading()
		return 0
	}
	// if self.FromPeer.FileInfo.Pieces()

	// Mark ourselves as downloading
	self.FromPeer.MarkAsDownloading()
	self.ToPeer.MarkAsDownloading()
	// Dequeue the zeroth piece
	var dequeuedPieceIdx int
	dequeuedPieceIdx, self.piecesToDownloadQueue = self.piecesToDownloadQueue[0],
		self.piecesToDownloadQueue[1:]
	// Try to reserve it.
	// If we couldn't, try the next one until we can
	if !availablePeerPieces.TryToReserve(dequeuedPieceIdx, self.ToPeer.StringifyID()) {
		print.Debugf("Failed to reserve piece [%+v]. Trying the next one...\n",
			dequeuedPieceIdx)
		goto looper
	}
	// If we reach here, 'dequeuedPiece' has been reserved: get all the piece's
	// blocks and run 'requestBlockFunc' on each. If any of them fails
	// immediately, this would mean there's a socket issue. Return with an
	// error and end the routine.
	print.Debugf("Peer [%s] successfully reserved piece [%+v]\n",
		self.ToPeer, dequeuedPieceIdx)
	dequeuedPiece := self.FromPeer.FileInfo.Pieces.Get(dequeuedPieceIdx)
	blocks := dequeuedPiece.Blocks()
	// Run 'requestBlockFunc' on every block
	// XXX This will effectively make a new goroutine per block. We'll need the
	// WaitGroup to know when all of them are done
	for _, block := range blocks {
		go self.blockRequester.request(block, errChan)
		time.Sleep(200 * time.Millisecond)
	}

	return len(blocks)
}

func filterPeerMsgsOfType(peerMsgs []PeerMsg, peerMsgToFilter PeerMsg) []PeerMsg {
	if len(peerMsgs) == 0 || peerMsgToFilter == nil {
		return nil
	}

	typedMsgs := []PeerMsg{}
	for _, msg := range peerMsgs {
		if reflect.TypeOf(msg) == reflect.TypeOf(peerMsgToFilter) {
			typedMsgs = append(typedMsgs, msg)
		}
	}

	return typedMsgs
}
