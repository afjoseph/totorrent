package peer

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/afjoseph/commongo/print"
	"github.com/anacrolix/dht/krpc"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/fileinfo"
)

type PeerMsgFunc func(*PeerConnection, PeerMsg, chan error)
type SendHandshakeFunc func(*PeerConnection, *metainfo.MetaInfo) error
type OnConnectFunc func(*PeerConnection) error
type Peer struct {
	Name string // This is just for debugging purposes
	IP   net.IP
	Port int
	ID   [20]byte
	// XXX 'listener' is used only if we're listening for connections
	listener          net.Listener
	FileInfo          *fileinfo.FileInfo
	PeerConnections   []*PeerConnection
	PeerMsgFunc       PeerMsgFunc
	OnConnectFunc     OnConnectFunc
	SendHandshakeFunc SendHandshakeFunc
	IsWeak            bool
	isDownloading     bool
	mutex             sync.Mutex
	// XXX All fields below are used exclusively for testing
	doTrackMsgs      bool
	inboundPeerMsgs  []PeerMsg
	outboundPeerMsgs []PeerMsg
}

type Peers []*Peer

func (self *Peer) MarkAsDownloading() {
	print.DebugFunc()
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.isDownloading = true
}

func (self *Peer) MarkAsNotDownloading() {
	print.DebugFunc()
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.isDownloading = false
}

func (self *Peer) IsDownloading() bool {
	print.DebugFunc()
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.isDownloading
}

func (self *Peer) MakeWeak() {
	print.Debugf("Marking Peer [%s] as weak\n", self)
	self.IsWeak = true
}

func (self *Peer) Close() error {
	print.DebugFunc()

	// Close all peerConnections
	for _, peerConn := range self.PeerConnections {
		err := peerConn.Conn.Close()
		if err != nil {
			return err
		}
	}
	// Close listener, if any
	// TODO will this segfault if it's empty?
	if self.listener != nil {
		self.listener.Close()
	}
	return nil
}

// StringifyID provides a simple way to print a peer's ID: since there might be
// non-ascii characters in a peer's ID, this can mess up a terminal's stdout
// when debugging things: things function uses an MD5 sum of the ID while
// retaining the client's information
func (self *Peer) StringifyID() string {
	var id strings.Builder
	id.Write(self.ID[:8])
	hash := md5.Sum(self.ID[8:])
	id.WriteString(hex.EncodeToString(hash[:]))
	return id.String()
}

func (self *Peers) UnmarshalBencode(b []byte) (err error) {
	var _v interface{}
	err = bencode.Unmarshal(b, &_v)
	if err != nil {
		return
	}
	switch v := _v.(type) {
	case string:
		var cnas krpc.CompactIPv4NodeAddrs
		err = cnas.UnmarshalBinary([]byte(v))
		if err != nil {
			return
		}
		for _, cp := range cnas {
			*self = append(*self, &Peer{
				IP:   cp.IP[:],
				Port: int(cp.Port),
			})
		}
		return
	case []interface{}:
		for _, i := range v {
			var p Peer
			p.FromDictInterface(i.(map[string]interface{}))
			*self = append(*self, &p)
		}
		return
	default:
		err = fmt.Errorf("unsupported type: %T", _v)
		return
	}
}

func (p *Peer) String() string {
	loc := net.JoinHostPort(p.IP.String(), fmt.Sprintf("%d", p.Port))
	if len(p.ID) != 0 {
		return fmt.Sprintf("%s:%x at %s", p.Name, p.ID, loc)
	} else {
		return loc
	}
}

// Set from the non-compact form in BEP 3.
func (p *Peer) FromDictInterface(d map[string]interface{}) {
	p.IP = net.ParseIP(d["ip"].(string))
	if _, ok := d["peer id"]; ok {
		var id [20]byte
		idAsSlice := []byte(d["peer id"].(string))
		copy(id[:], idAsSlice)
		p.ID = id
	}
	p.Port = int(d["port"].(int64))
}

func (p *Peer) FromNodeAddr(na krpc.NodeAddr) *Peer {
	p.IP = na.IP
	p.Port = na.Port
	return p
}

func MakePeerId() [20]byte {
	var buff bytes.Buffer
	b := make([]byte, 12)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	buff.WriteString("-BN0001-")
	buff.Write(b)
	idAsSlice := buff.Bytes()
	var id [20]byte
	copy(id[:], idAsSlice)
	return id
}

// Piece Download Strategy
// -----------------------
// The abstract design of the download routine is:
// * Got a HAVE msg for piece 2
// * self.peerConn[peer1].QueuePiece(pieceIdx, &conn, &targetPeer)
// 	* self.peerConn[peer1].AvailablePieces.append(pieceIdx=2)
// 	* If (!self.peerConn[peer1].IsDownloadRoutineActive()) { self.peerConn[peer1].StartDownloadRoutine() }
// 	* self.peerConn[peer1].StartDownloadRoutine()
// 		* Sequentially check self.peerConn[peer1].AvailablePieces to see which block can we download with this peer
// 			* loop over all blocks starting from the first piece
// 				* if no blocks can be downloaded, just end the function
// 				* Else, run DownloadBlock
// 					* Mark the block as DOWNLOADING
// 					* start a misfire timer
// 					* run a REQUEST peermsg
// 	* self.StartMisfireTimer
// 		* Wait for a bit
// 		* Check the misfire channel
// 		* If you get a misfire
// 			* mark that block as IDLE
// 			* run self.peerConn[peer1].StartDownloadRoutine() again
// NOTE: ALL accesses and setters to the FileInfo object will need to be regulated
// 		by some sort of channel system to manage multiple accesses. For now, if you see
// 		`self.FileInfo.whatever`, do consider the accesses to be "just working"
//
// A more concrete example is here:
// * One torrent file has 3 peers and 3 pieces, 2 blocks per piece
// * We make a client peer
// 	* that has 3 PeerConnection objects
// * client peer handshakes with peers and gets the following HAVE messages:
// 	* This is the default case: no hiccups
// 		* Got a HAVE msg from peer 1 for piece 2
// 		* self.peerConn[peer1].QueuePiece(pieceIdx, &conn, &targetPeer)
// 			* self.peerConn[peer1].AvailablePieces.append(pieceIdx=2)
// 			* If (!self.peerConn[peer1].IsDownloadRoutineActive()) { self.peerConn[peer1].StartDownloadRoutine() }
// 		* self.peerConn[peer1].StartDownloadRoutine()
// 			* Client checks and sees piece=2,block=1 is untouched
// 			* Client calls `self.peer.FileInfo.Pieces[2].Blocks[1].TryToDownload()` and it WORKS
// 			* Start a self.peerconn[peer1].misfireTimer(pieceIdx=2, blockIdx=1)
// 			* self.peerConn[peer1] builds and sends a REQUEST peermsg
// 		* we received a PIECE message before the timer runs out
// 			* We need to know which connection did this come from
// 				* Run something like `peerConn = FindPeerConnFromConn(self.peerConns, conn)`
// 			* self.peerconn[peer1].misfireChan <- 'all good'
// 			* self.peer.FileInfo.Pieces[2].Blocks[1].FinalizeBlock()
// 				* set as DOWNLOADED
// 				* save to filesystem
// 		* self.peerConn[peer1].StartMisfireTimer
// 			* Checked and nothing happened
// 	* This case has a misfire AND is downloading a block from a piece that's available in another PeerConn
// 		* Got a HAVE msg from peer 2 for piece 2
// 		* self.peerConn[peer2].QueuePiece(pieceIdx, &conn, &targetPeer)
// 			* self.peerConn[peer2].AvailablePieces.append(pieceIdx=2)
// 			* If (!self.peerConn[peer2].IsDownloadRoutineActive()) { self.peerConn[peer2].StartDownloadRoutine() }
// 		* self.peerConn[peer2].StartDownloadRoutine()
// 			* Client checks and sees piece=2,block=1 is not available (being actively downloaded by peer 1)
// 				* Loop once more and we find piece=2,block=2 is available
// 			* Client calls `self.peer.FileInfo.Pieces[2].Blocks[2].TryToDownload()` and it WORKS
// 			* Start a self.peerconn[peer2].misfireTimer(pieceIdx=2, blockIdx=2)
// 			* self.peerConn[peer2] builds and sends a REQUEST peermsg
// 		* we DID NOT receive a PIECE msg
// 		* self.peerConn[peer2].StartMisfireTimer(pieceIdx, blockIdx)
// 			* timer ticks for 3 seconds
// 			* timer ran out
// 			* We check (non-blocking) `self.peerconn[peer1].misfireChan` and we get nothing
// 			* We declare this to be a misfire
// 				* self.peer.FileInfo.Pieces[2].Blocks[2].SetAsIdle()
// 			* Run download routine again
// 				* self.peerConn[peer2].StartDownloadRoutine()
// 				* Tick 1 in the misfire counter
// 					* If we get 3, this peer is bad: we won't try to play with them
// 					* Run peerConn.KillDueToBadConnection()
// 		* Download routine will try to donwload the same piece most probably (if nobody touched it)

// TODO EVERYTHING BELOW THIS NEEDS A REWORK (_[21-05-31 Mon 10:39 AM]_)
// -----------------------------------------
// // peer.go
// func (self peer) HandleHaveMsg (peermsg, conn, targetPeer) {
// 	pieceIdx = peermsg.(HavePeerMsg).GetPieceIdx()
// 	if self.FileInfo.Pieces[pieceIdx].Downloaded() {
// 		skip
// 	}
// 	self.AddToJobQueue(NewJob(pieceIdx, &conn, &targetPeer))
// 	if self.IsJobQueueEmpty(peerConn.ToPeer.StringifyID()) {
// 		self.StartJobQueue(peerConn.ToPeer.StringifyID())
// 	}
// }
//
// // XXX In our scheme, we maintain ONE download request per peer
// func (self peer) HandlePieceMsg (peermsg, conn, targetPeer) {
// 	// TODO shutdown misfire counter
// 	piecePeerMsg := peermsg.(PiecePeerMsg)
// 	pieceIdx = piecePeerMsg.GetPieceIdx()
// 	self.FileInfo.Pieces[pieceIdx].CopyBlockToFileSystem(piecePeerMsg)
// }
//
// func (self peer) AddToJobQueue(pieceIdx) {
// 	TODO
// }
//
// func (self peer) IsJobQueueEmpty(pieceIdx) {
// 	TODO
// }
//
// // XXX This is in Peer struct
// peersAndPiecesMap := map[string]Pieces // peerId -> Pieces
// peersAndJobQueuesMap := map[string]JobQueue // peerId -> jobQueue
//
// func (self peer) StartMisfireCounter() {
// 	const misfireDuration = 5*time.Second
// 	time.Sleep(misfireDuration)
// }
//
// func (self peer) StartJobQueue(peerId) {
// 	// FIXME Is this gonna change `targetPiece` or 'self.FileInfo.Pieces[self.JobQueue.pieceIdx]'?
// 	while (!self.JobQueue.Empty()) {
// 		targetJob := self.JobQueue.Dequeue()
// 		targetPiece := self.FileInfo.Pieces[targetJob.PieceIdx]
// 		if !targetPiece.Available() {
// 			// Move on to the next one
// 			continue
// 		}
// 		// Pass peer ID so we know who's taking it
// 		targetPiece.MarkAsBusy(peer.StringifyID())
//
// 		go self.StartMisfireCounter()
// 		go func() {
// 			msg := buildPeerRequestMsg(targetJob.PieceIdx)
// 			targetJob.Conn.Write(msg)
// 		}
// 	}
// }

// listenForPeers will ASYNCHRONOUSLY establish a TCP socket and listen for new
// connections. When a new connection is established, it'll wrap it in a
// PeerConnection and start listening for messages.
//
// XXX Contrary to connectWithPeer(), this function actually can spawn MORE
// THAN ONE PeerConnection for any peer that talks with it
func (self *Peer) ListenForPeers(
	myMetainfo *metainfo.MetaInfo,
	errChan chan PeerAndError,
) error {
	print.DebugFunc()

	// Make a listener
	var err error
	self.listener, err = net.Listen("tcp4",
		fmt.Sprintf("%s:%d", self.IP.String(), self.Port))
	if err != nil {
		return print.ErrorWrapf(ERR_LISTEN_FOR_PEERS, err.Error())
	}

	// Process:
	// * ASYNCHRONOUSLY start an infinite loop, listening for new connections
	// * When a new connection is accept, run start ASYNCHRONOUSLY listening to
	// messages from it and have the original goroutine listen for new
	// connections again
	// * When we call peer.Close():
	//     * no new connections will be accepted and `acceptConn()` will return
	//     an error, short-circuiting the infinite for loop
	//     * Also, existing reading from existing PeerConnections will receive
	//     EOF, short-circuiting peerConn.listenForMessages
	go func() {
		for {
			// Accept connections
			// XXX This is a BLOCKING call
			peerConn, err := acceptConn(self, self.listener, errChan)
			if err != nil {
				errChan <- PeerAndError{self, err}
				return
			}
			self.PeerConnections = append(self.PeerConnections, peerConn)

			// Run OnConnectFunc, if any
			if self.OnConnectFunc != nil {
				go func() {
					err = self.OnConnectFunc(peerConn)
					if err != nil {
						errChan <- PeerAndError{self, err}
						return
					}
				}()
			}

			// Run SendHandshakeFunc, if any
			if self.SendHandshakeFunc != nil {
				go func() {
					err = self.SendHandshakeFunc(peerConn, myMetainfo)
					if err != nil {
						errChan <- PeerAndError{self, err}
						return
					}
				}()
			}

			// Start listener
			go peerConn.listenForMessages(errChan)
			// go func() {
			// 	// XXX This is a BLOCKING call
			// 	// peerConn.Conn.Close()
			// }()
		}
	}()
	return nil
}

type OnPeerErrorFunc func(*Peer, error)

// ConnectWithAllPeers handshakes and starts the Bittorrent protocol with all
// 'peers' (limited by 'limit') using the info in 'myMetainfo'
// XXX Each peer connection is really coupled with ONE torrent file: this would
// mean that if we're working with more than one torrent file but talking to
// the same exact peer, there's zero optimization. That's fine for now
func (self *Peer) ConnectWithAllPeers(
	myMetainfo *metainfo.MetaInfo,
	targetPeers []*Peer,
	maxPeerLimit int, // XXX 0 means no limit
	onPeerErrorFunc OnPeerErrorFunc,
) error {
	print.DebugFunc()

	// Check for peers
	if len(targetPeers) == 0 {
		// TODO: Technically: if we get this, it'll be wise to ask another
		// tracker for more peers, but I don't know if this is according to the
		// bittorrent specs; if every client did that, it could lead to
		// trackers being harassed constantly
		return print.ErrorWrapf(ERR_CONNECT_PEERS,
			"Found no usable peers for this torrent")
	}

	// Connect with each peer
	peersTried := 0
	peersDoneChan := make(chan *Peer, 10)
	peersErrChan := make(chan PeerAndError, 10)
	for _, targetPeer := range targetPeers {
		if maxPeerLimit != 0 && peersTried > maxPeerLimit {
			break
		}
		go self.connectWithPeer(myMetainfo, targetPeer,
			peersDoneChan, peersErrChan)
		peersTried += 1
	}

	// Enter a loop until all peers are done
	peersRemaining := peersTried
	for {
		if peersRemaining == 0 {
			break
		}
		select {
		case peerAndError := <-peersErrChan:
			// XXX Currently, we ignore ALL peer errors
			print.Warnf("Received error from peer [%v]: %v\n",
				peerAndError.Peer, peerAndError.Error)
			if onPeerErrorFunc != nil {
				onPeerErrorFunc(peerAndError.Peer, peerAndError.Error)
			}
			peerAndError.Peer.Close()
			peersRemaining--
		case donePeer := <-peersDoneChan:
			print.Debugf("Peer %v is done\n", donePeer)
			donePeer.Close()
			peersRemaining--
		default:
			print.Debugf("Client: waiting for all peer connections to finalize\n")
			time.Sleep(3 * time.Second)
		}
	}
	print.Debugf("Done with all peers\n")
	return nil
}

func DefaultSendHandshakeFunc(peerConn *PeerConnection,
	myMetainfo *metainfo.MetaInfo) error {
	print.DebugFunc()

	peerConn.WritePeerMsg(BuildPeerHandshakeMsg(peerConn.ToPeer.ID,
		sha1.Sum(myMetainfo.InfoBytes)))
	return nil
}
