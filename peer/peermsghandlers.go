package peer

import (
	"fmt"
	"log"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/util"
)

// DefaultPeerMsgFunc
func DefaultPeerMsgFunc(peerConn *PeerConnection, peerMsg PeerMsg, errChan chan error) {
	log.Printf("DefaultPeerMsgFunc peerMsg: %v\n", peerMsg)
	// Handle peerMsg based on the type
	// TODO consider using a polymorphic struct and just running
	// peerMsg.Handle(): would this be easy to test?
	// Furthermore, I don't know if one of those message would actually require
	// more parameters that others
	var err error
	if typedPeerMsg, ok := peerMsg.(*HandshakePeerMsg); ok {
		err = peerConn.FromPeer.handlePeerHandshakeMessage(typedPeerMsg, peerConn, errChan)
	} else if typedPeerMsg, ok := peerMsg.(*KeepAlivePeerMsg); ok {
		err = peerConn.FromPeer.handlePeerKeepAliveMessage(typedPeerMsg, peerConn)
	} else if typedPeerMsg, ok := peerMsg.(*InterestedPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerInterestedMessage(typedPeerMsg, peerConn)
	} else if typedPeerMsg, ok := peerMsg.(*NotInterestedPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerNotInterestedMessage(typedPeerMsg, peerConn)
	} else if typedPeerMsg, ok := peerMsg.(*UnchokedPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerUnchokedMessage(typedPeerMsg, peerConn)
	} else if typedPeerMsg, ok := peerMsg.(*ChokedPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerChokedMessage(typedPeerMsg, peerConn)
	} else if typedPeerMsg, ok := peerMsg.(*HavePeerMsg); ok {
		err = peerConn.FromPeer.handlePeerHaveMessage(typedPeerMsg, peerConn, errChan)
	} else if typedPeerMsg, ok := peerMsg.(*BitfieldPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerBitfieldMessage(typedPeerMsg, peerConn, errChan)
	} else if typedPeerMsg, ok := peerMsg.(*RequestPeerMsg); ok {
		err = peerConn.FromPeer.handlePeerRequestMessage(typedPeerMsg, peerConn, errChan)
	} else if typedPeerMsg, ok := peerMsg.(*PiecePeerMsg); ok {
		err = peerConn.FromPeer.handlePeerPieceMessage(typedPeerMsg, peerConn, errChan)
	} else {
		panic("Peermsg parsing happened before: we must never reach here")
	}
	if err != nil {
		errChan <- err
	}
}

// handlePeerHandshakeMessage basically sends an "interested" message to the
// peer
func (self *Peer) handlePeerHandshakeMessage(
	peerMsg *HandshakePeerMsg, peerConn *PeerConnection, errChan chan error) error {
	print.Debugf(
		"Received HANDSHAKE. Sending INTERESTED & BITFIELD on %v\n",
		peerConn)

	// Send INTERESTED msg
	peerConn.WritePeerMsg(BuildPeerInterestedMsg())
	// Send BITFIELD msg
	if len(self.FileInfo.Pieces.DownloadedPieceIndices()) == 0 {
		print.Warnf(
			"Peer [%s] has no downloaded pieces: will not send BITFIELD msg during HANDSHAKE protocol\n",
			self)
	} else {
		msg, err := BuildPeerBitfieldMsg(self.FileInfo.Pieces)
		if err != nil {
			// XXX Ignore this error: if we can't make a bitfield msg, that's fine
			print.Warnf(fmt.Sprintf("%s: will NOT kill peer\n", err.Error()))
			return nil
		}
		peerConn.WritePeerMsg(msg)
	}
	return nil
}

func (self *Peer) handlePeerKeepAliveMessage(
	peerMsg *KeepAlivePeerMsg, peerConn *PeerConnection) error {
	print.Debugf("Received KEEP_ALIVE message %s on %v\n",
		peerConn.ToPeer, peerConn.Conn.RemoteAddr())
	// XXX Do nothing
	return nil
}

func (self *Peer) handlePeerRequestMessage(peerMsg *RequestPeerMsg,
	peerConn *PeerConnection, errChan chan error) error {
	print.Debugf("Received REQUEST message %s on %v\n",
		peerConn.ToPeer, peerConn.Conn.RemoteAddr())
	b, err := peerMsg.GetBlock()
	if err != nil {
		return err
	}
	print.Debugf(
		"Peer [%s]: Received REQUEST from peer. Sending PIECE msg %v...\n",
		peerConn.FromPeer, b)
	fullBlock, err := peerConn.FromPeer.FileInfo.GetBlock(
		b.PieceIdx,
		b.Begin, b.Length,
		peerConn.FromPeer.FileInfo.IsSeeding)
	if err != nil {
		return err
	}
	peerConn.WritePeerMsg(BuildPeerPieceMsg(fullBlock))
	return nil
}

// * handlePeerPieceMsg()
//   * `peerConn.BlockRequests[block.idx] <- true`
//     * To shutoff the misfire timer
//   * put the DONE state for that block
//   * put that block in the file system
//   * if `peerConn.BlockRequests.IsEmpty()`
//     * we have no more active requested blocks
//     * Call `piece.MarkPieceAsDownloaded()`
//     * Call `peerConn.RequestNextPiece()` to request the next piece
func (self *Peer) handlePeerPieceMessage(peerMsg *PiecePeerMsg,
	peerConn *PeerConnection, errChan chan error) error {
	b, err := peerMsg.GetBlock()
	if err != nil {
		return err
	}
	print.Debugf("handlePeerPieceMessage: Received BLOCK %v on %v\n",
		b, peerConn)
	peerConn.blockRequester.onBlockReceive(b)
	err = peerConn.FromPeer.FileInfo.SaveBlock(b)
	if err != nil {
		return err
	}

	print.Debugf("Total blocks left for piece [%d]: %d\n",
		b.PieceIdx, peerConn.blockRequester.BlocksLeft())
	if peerConn.blockRequester.BlocksLeft() == 0 {
		print.Debugf(
			"No more blocks left for piece [%d]: requesting next piece...\n",
			b.PieceIdx)
		peerConn.blockRequester.reset()
		peerConn.FromPeer.FileInfo.Pieces.MarkAsDone(int(b.PieceIdx))
		piecesLeft := peerConn.FromPeer.FileInfo.Pieces.TotalUndonePiecesLeft()
		print.Debugf("Total pieces undone: %d\n", piecesLeft)
		if piecesLeft != 0 {
			peerConn.RequestNextPiece(peerConn.FromPeer.FileInfo.Pieces, errChan)
		} else {
			peerConn.FromPeer.MarkAsNotDownloading()
			peerConn.ToPeer.MarkAsNotDownloading()
			err := peerConn.FromPeer.FileInfo.FinalizeFunc(peerConn.FromPeer.FileInfo)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (self *Peer) handlePeerBitfieldMessage(peerMsg *BitfieldPeerMsg,
	peerConn *PeerConnection, errChan chan error) error {
	wasQueueEmpty := len(peerConn.piecesToDownloadQueue) == 0
	pieceIndices, err := peerMsg.GetPieceIndices(
		peerConn.FromPeer.FileInfo.Pieces.MaxPieceIdx())
	if err != nil {
		return err
	}
	if len(pieceIndices) == 0 {
		print.Warnf(
			"Received BITFIELD msg with an empty slice on %v. Doing nothing.\n",
			peerConn)
		return nil
	}
	print.Debugf(
		"Received BITFIELD msg with pieces [%v] on %v\n",
		pieceIndices, peerConn)
	peerConn.piecesToDownloadQueue = util.IntSliceUniqueMergeSlices(
		peerConn.piecesToDownloadQueue, pieceIndices)
	// If this is the first time we've added to the queue, start downloading
	if wasQueueEmpty {
		// Start from the first always
		pieceIdx := peerConn.piecesToDownloadQueue[0]
		peerConn.blockRequester = NewBlockRequester(peerConn,
			peerConn.FromPeer.FileInfo.Pieces.Get(pieceIdx).BlockCount(),
			DefaultWasBlockReceived,
			nil,
		)
		peerConn.RequestNextPiece(
			peerConn.FromPeer.FileInfo.Pieces,
			errChan)
	}
	return nil
}

func (self *Peer) handlePeerHaveMessage(peerMsg *HavePeerMsg,
	peerConn *PeerConnection, errChan chan error) error {

	wasQueueEmpty := len(peerConn.piecesToDownloadQueue) == 0
	pieceIdx := peerMsg.GetPieceIdx()
	if util.IntSliceHas(peerConn.piecesToDownloadQueue, int(pieceIdx)) {
		print.Warnf(
			"Received HAVE msg with duplicate pieceIdx [%d] on %v. Doing nothing.\n",
			pieceIdx, peerConn)
		return nil
	}
	print.Debugf(
		"Received HAVE message with pieceIdx [%d] on %v\n",
		pieceIdx, peerConn)
	peerConn.piecesToDownloadQueue = append(
		peerConn.piecesToDownloadQueue, int(pieceIdx))
	// If this is the first piece in the queue, start downloading
	if wasQueueEmpty {
		peerConn.blockRequester = NewBlockRequester(peerConn,
			peerConn.FromPeer.FileInfo.Pieces.Get(pieceIdx).BlockCount(),
			DefaultWasBlockReceived,
			nil,
		)
		peerConn.RequestNextPiece(
			peerConn.FromPeer.FileInfo.Pieces,
			errChan)
	}
	return nil
}

func (self *Peer) handlePeerInterestedMessage(
	peerMsg *InterestedPeerMsg, peerConn *PeerConnection) error {
	print.Debugf(
		"Received INTERESTED message. Sending UNCHOKED on %v\n",
		peerConn)

	peerConn.WritePeerMsg(BuildPeerUnchokedMsg())
	return nil
}

func (self *Peer) handlePeerUnchokedMessage(peerMsg *UnchokedPeerMsg, peerConn *PeerConnection) error {
	print.Debugf("Received UNCHOKED message %s on %v\n",
		peerConn.ToPeer, peerConn.Conn.RemoteAddr())
	// XXX Do nothing
	return nil
}

func (self *Peer) handlePeerChokedMessage(peerMsg *ChokedPeerMsg, peerConn *PeerConnection) error {
	print.Debugf("Received CHOKED message %s on %v\n",
		peerConn.ToPeer, peerConn.Conn.RemoteAddr())
	// Drop the peerConnection immediately
	peerConn.Conn.Close()
	return nil
}

func (self *Peer) handlePeerNotInterestedMessage(peerMsg *NotInterestedPeerMsg,
	peerConn *PeerConnection) error {
	print.Debugf("Received NOT_INTERESTED message %s on %v\n",
		peerConn.ToPeer, peerConn.Conn.RemoteAddr())
	// Drop the peerConnection immediately
	peerConn.Conn.Close()
	return nil
}
