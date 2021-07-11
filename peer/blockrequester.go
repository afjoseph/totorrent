package peer

import (
	"errors"
	"sync"
	"time"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/piece"
)

const BlockRequestReceiveTimeout = time.Duration(15 * time.Second)
const BlockRequestTotalAllowedMisfires = 3

var ERR_REQUEST_BLOCK = errors.New("ERR_REQUEST_BLOCK")

type WasBlockReceivedFunc func(chan bool) bool

type BlockRequester struct {
	peerConn                *PeerConnection
	data                    map[int]chan bool
	fullPieceDownloadedChan chan int
	mutex                   sync.Mutex
	totalNumOfBlocks        int
	blocksLeft              int
	wasBlockReceivedFunc    WasBlockReceivedFunc
}

func NewBlockRequester(
	peerConn *PeerConnection,
	totalNumOfBlocks int,
	wasBlockReceivedFunc WasBlockReceivedFunc,
	fullPieceDownloadedChan chan int,
) *BlockRequester {
	if fullPieceDownloadedChan != nil && cap(fullPieceDownloadedChan) == 0 {
		panic("Only buffered channels are accepted here so as not to block sending. Ideally, initialize it to the number of total pieces")
	}
	return &BlockRequester{
		peerConn:                peerConn,
		data:                    make(map[int]chan bool),
		fullPieceDownloadedChan: fullPieceDownloadedChan,
		totalNumOfBlocks:        totalNumOfBlocks,
		blocksLeft:              totalNumOfBlocks,
		wasBlockReceivedFunc:    wasBlockReceivedFunc,
	}
}

func (self *BlockRequester) reset() {
	print.DebugFunc()
	self.blocksLeft = self.totalNumOfBlocks
}

func (self *BlockRequester) request(targetBlock *piece.Block, errChan chan error) {
	print.Debugf("Requesting %v on %v...\n", targetBlock, self.peerConn)
	self.mutex.Lock()
	self.data[targetBlock.Idx] = make(chan bool, 10)
	self.mutex.Unlock()

	requestPeerMsg, err := BuildPeerRequestMsg(targetBlock)
	if err != nil {
		errChan <- err
		return
	}
	self.peerConn.WritePeerMsg(requestPeerMsg)

	// This async func will:
	// * Synchronously check if the block was received
	// * If it was, do nothing
	// * If it wasn't
	//   * Increment the misfire
	//   * See if we need to declare this peer as "Weak"
	//   * Re-run the Request() func
	go func() {
		// XXX This is a BLOCKING call that will timeout after X seconds
		if self.wasBlockReceivedFunc(self.data[targetBlock.Idx]) {
			print.Debugf("%v was received: misfire dismissed\n", targetBlock)
			return
		}
		print.Debugf("%v was NOT received: incrementing misfires...\n",
			targetBlock)
		self.peerConn.misfires++
		if self.peerConn.misfires > BlockRequestTotalAllowedMisfires {
			print.Debugf(
				"peerConn [%v] received maximum num of misfires. Declaring peer [%s] as weak\n",
				self.peerConn.String(),
				self.peerConn.ToPeer,
			)
			self.peerConn.ToPeer.MakeWeak()
			// peerConn.ToPeer.Close()
			return
		}
		print.Debugf("Rerunning request() for %v...\n", targetBlock)
		self.request(targetBlock, errChan)
	}()
}

// onBlockReceive locks the data structure, and sends a signal to the
// misfire channel respective to blockIdx. It also decreases the total
// blocks left: if we reach zero: send a signal to doneChan: all blocks
// were downloaded successfully
// This is triggered by handlePeerPieceMsg()
func (self *BlockRequester) onBlockReceive(targetBlock *piece.Block) {
	print.Debugf("Received %v. Disarming misfire channel...\n", targetBlock)
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.data[targetBlock.Idx] <- true
	close(self.data[targetBlock.Idx])
	self.blocksLeft--
	if self.blocksLeft == 0 {
		print.Debugf(
			"No more blocks left in piece #%d on %v\n",
			targetBlock.PieceIdx,
			self.peerConn)
		if self.fullPieceDownloadedChan != nil {
			print.Debugf(
				"Sending signal to fullPieceDownloadedChan for piece #%d on %v\n",
				targetBlock.PieceIdx,
				self.peerConn)
			self.fullPieceDownloadedChan <- int(targetBlock.PieceIdx)
		}
	}
}

func (self *BlockRequester) BlocksLeft() int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.blocksLeft
}

// This is a blocking call: we'll either wait for when we receive some data
// from handlePeerPieceMsg(), or when a timeout occurs
func DefaultWasBlockReceived(misfireChan chan bool) bool {
	select {
	case <-misfireChan:
		return true
	case <-time.After(BlockRequestReceiveTimeout):
		return false
	}
}
