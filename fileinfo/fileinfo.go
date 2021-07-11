package fileinfo

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/afjoseph/commongo/print"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/util"
)

var ERR_FILEINFO_READ_DOWNLOADED_DATA = errors.New("ERR_FILEINFO_READ_DOWNLOADED_DATA")
var ERR_FILEINFO_ASSEMBLE_BLOCKS = errors.New("ERR_FILEINFO_ASSEMBLE_BLOCKS")
var ERR_FILEINFO_ASSEMBLE_BLOCKS__ALREADY_EXISTS = errors.New(
	"ERR_FILEINFO_ASSEMBLE_BLOCKS__ALREADY_EXISTS")

type FinalizeFunc func(*FileInfo) error

// For simplicity, we can limit one peer to one torrent
//   since the AnnounceResponse gives me back specific peers for a specific
//   torrent, we can couple a peer with a torrent cleanly
type FileInfo struct {
	DownloadedBlocksDir string
	// TODO For now, this is only accommodating for single-file torrents
	FinalDownloadDir string
	IsSeeding        bool
	Pieces           piece.SafePieces
	torrentInfo      *metainfo.Info
	FinalizeFunc     FinalizeFunc
}

// NewFileInfo takes 'totalPieces' and 'pieceLength' and constructs blocks and
// pieces out of it
// XXX We're NOT allocating empty bytes for each block: this will be very
// wasteful: we will just write directly to disk
func NewFileInfo(
	totalPieces int,
	pieceLength int64,
	blockLength int,
	finalDownloadDir string,
	downloadedBlocksDir string,
	torrentInfo *metainfo.Info,
	finalizeFunc FinalizeFunc,
	isSeeding bool,
) (*FileInfo, error) {
	fileInfo := &FileInfo{
		torrentInfo:         torrentInfo,
		DownloadedBlocksDir: downloadedBlocksDir,
		IsSeeding:           isSeeding,
		FinalizeFunc:        finalizeFunc,
	}
	var err error
	if finalDownloadDir != "" {
		if !util.IsDirectory(finalDownloadDir) {
			util.MkdirAllOrPanic(finalDownloadDir)
		}
		fileInfo.FinalDownloadDir, err = filepath.Abs(finalDownloadDir)
		if err != nil {
			return nil, err
		}
	}
	// Calculate all regular pieces
	fileInfo.Pieces = piece.NewSafePieces()
	for pieceIdx := 0; pieceIdx < totalPieces-1; pieceIdx++ {
		fileInfo.Pieces.Put(piece.CalculateNewPiece(
			pieceIdx, pieceLength,
			blockLength, isSeeding))
	}
	// Calculate the last piece
	remainingLength := torrentInfo.Length % pieceLength
	if remainingLength != 0 {
		fileInfo.Pieces.Put(piece.CalculateNewPiece(
			totalPieces-1, remainingLength,
			blockLength, isSeeding))
	} else {
		fileInfo.Pieces.Put(piece.CalculateNewPiece(
			totalPieces-1, pieceLength,
			blockLength, isSeeding))
	}
	// Mark last piece
	fileInfo.Pieces.SetAsLastPiece(totalPieces - 1)

	return fileInfo, nil
}

func NewFileInfoFilledWithRandomData(
	totalPieces int,
	pieceLength int64,
	blockLength int,
	finalDownloadDir string,
	downloadedBlocksDir string,
	torrentInfo *metainfo.Info,
	finalizeFunc FinalizeFunc,
	isSeeding bool,
) (*FileInfo, error) {
	myFileInfo, err := NewFileInfo(totalPieces,
		pieceLength, blockLength,
		finalDownloadDir,
		downloadedBlocksDir,
		torrentInfo, finalizeFunc, isSeeding)
	if err != nil {
		return nil, err
	}
	for i := 0; i < myFileInfo.Pieces.Len(); i++ {
		p := myFileInfo.Pieces.Get(i)
		if p == nil {
			panic("Nil Piece")
		}
		blocks := p.Blocks()
		for _, b := range blocks {
			b.Buff = make([]byte, b.Length)
			_, err := rand.Read(b.Buff)
			if err != nil {
				return nil, err
			}
		}
		if isSeeding {
			myFileInfo.Pieces.MarkAsDone(p.Idx())
		}
	}
	return myFileInfo, nil
}

func (self *FileInfo) GetBlock(
	pieceIdx int,
	blockBegin uint32,
	blockLength uint32,
	isSeeding bool,
) (*piece.Block, error) {
	var err error
	p := self.Pieces.Get(pieceIdx)
	if p == nil {
		return nil, nil
	}
	b := p.GetBlockFromOffset(blockBegin, blockLength)
	if b == nil {
		return nil, nil
	}
	// If we're seeding, we can add the block's bytes as well
	if isSeeding {
		b.Buff, err = readFromDownloadedData(
			self.FinalDownloadDir,
			p.Idx(), b.Idx, p.BlockCount(),
			b.Begin, b.Length, self.torrentInfo.IsDir())
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

func readFromDownloadedData(
	finalDownloadDir string,
	pieceIdx, blockIdx, blocksPerPieceCount int,
	blockBegin, blockLength uint32,
	isDir bool,
) ([]byte, error) {
	if isDir {
		panic("TODO")
	} else {
		return readFromDownloadedData_singlefilemode(
			finalDownloadDir,
			pieceIdx, blockIdx, blocksPerPieceCount,
			blockBegin, blockLength)
	}
}

func readFromDownloadedData_singlefilemode(
	finalDownloadDir string,
	pieceIdx, blockIdx, blocksPerPieceCount int,
	blockBegin, blockLength uint32,
) ([]byte, error) {
	dirEntry, err := os.ReadDir(finalDownloadDir)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_FILEINFO_READ_DOWNLOADED_DATA, err.Error())
	}
	// TODO You're gonna get some painintheass from .DS_Store here
	if len(dirEntry) != 1 {
		return nil, print.ErrorWrapf(ERR_FILEINFO_READ_DOWNLOADED_DATA,
			"Single mode file does not have single file")
	}
	targetFilepath := filepath.Join(finalDownloadDir, dirEntry[0].Name())
	print.Debugf("Reading data from %s...\n", targetFilepath)
	fd, err := os.Open(targetFilepath)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_FILEINFO_READ_DOWNLOADED_DATA, err.Error())
	}
	defer fd.Close()
	buff := make([]byte, int(blockLength))
	seekLoc := (pieceIdx * blocksPerPieceCount) + blockIdx
	seekLoc *= int(blockLength)
	_, err = fd.Seek(int64(seekLoc), 0)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_FILEINFO_READ_DOWNLOADED_DATA, err.Error())
	}
	_, err = fd.Read(buff)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_FILEINFO_READ_DOWNLOADED_DATA, err.Error())
	}
	return buff, nil
}

func (self *FileInfo) SaveBlock(targetBlock *piece.Block) error {
	path := filepath.Join(
		self.DownloadedBlocksDir,
		fmt.Sprintf("block_%010d_%010d", targetBlock.PieceIdx, targetBlock.Idx),
	)
	print.Debugf("Saving [%v] to [%s]...\n", targetBlock, path)
	err := os.WriteFile(
		path, targetBlock.Buff,
		0644,
	)
	if err != nil {
		return err
	}
	return nil
}

func (self *FileInfo) DeleteBlocksDir() {
	os.RemoveAll(self.DownloadedBlocksDir)
}

func (self *FileInfo) DeleteFinalDownloadDir() {
	os.RemoveAll(self.FinalDownloadDir)
}

func assembleBlocks_singlefilemode(
	downloadedBlocksDir, finalDownloadDir, finalDownloadName string,
	isDir bool,
) error {
	dirEntry, err := os.ReadDir(downloadedBlocksDir)
	if err != nil {
		return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
	}
	finalDownloadFilepath, err := filepath.Abs(
		filepath.Join(finalDownloadDir, finalDownloadName))
	if err != nil {
		return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
	}
	// Check if file already exists so as not to overwrite it
	if util.IsFile(finalDownloadFilepath) {
		print.Warnf("Download already assembled at %s\n",
			finalDownloadFilepath)
		return print.ErrorWrapf(
			ERR_FILEINFO_ASSEMBLE_BLOCKS__ALREADY_EXISTS,
			"File already exists: %s",
			finalDownloadFilepath,
		)
	}
	finalDownloadFile, err := os.Create(finalDownloadFilepath)
	if err != nil {
		return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
	}
	defer finalDownloadFile.Close()
	// Find the relevant files and collect their paths: we'll need to sort them
	// to ensure the data's order remains intact
	sortedChunkPaths := []string{}
	for _, file := range dirEntry {
		chunkPath, err := filepath.Abs(filepath.Join(downloadedBlocksDir, file.Name()))
		if err != nil {
			return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
		}
		if !strings.HasPrefix(file.Name(), "block") {
			print.Warnf("Ignoring non-block file [%s] in downloadedBlocksDir [%s]...\n",
				chunkPath, downloadedBlocksDir)
		}
		sortedChunkPaths = append(sortedChunkPaths, chunkPath)
	}
	sort.Strings(sortedChunkPaths)
	// For every file, copy its contents to finalDownloadFile
	for _, chunkPath := range sortedChunkPaths {
		print.Debugf("Assembling %v...\n", chunkPath)
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
		}
		// XXX This will append, not overwrite content
		_, err = io.Copy(finalDownloadFile, chunkFile)
		if err != nil {
			return print.ErrorWrapf(ERR_FILEINFO_ASSEMBLE_BLOCKS, err.Error())
		}
		chunkFile.Close()
		os.Remove(chunkPath)
	}
	print.Debugf("Assembled all blocks to %s\n", finalDownloadFilepath)
	return nil
}

func assembleBlocks(
	downloadedBlocksDir, finalDownloadDir, finalDownloadName string,
	isDir bool,
) error {
	if isDir {
		panic("TODO")
	} else {
		return assembleBlocks_singlefilemode(downloadedBlocksDir,
			finalDownloadDir, finalDownloadName, isDir)
	}
}

// Finalize is called when all self.Pieces are done downloading.
// It is called from the PIECE msg handler of the last PIECE msg we receive.
// It does the following:
// * Assemble all downloaded blocks (in 'self.DownloadedBlocksDir') to
//   'self.FinalDownloadDir'
// * Enters 'seeding' state
func DefaultFinalizeFunc(fileInfo *FileInfo) error {
	print.Debugf("Finalizing fileinfo...\n")
	err := assembleBlocks(fileInfo.DownloadedBlocksDir,
		fileInfo.FinalDownloadDir, fileInfo.torrentInfo.Name,
		fileInfo.torrentInfo.IsDir())
	switch {
	case errors.Is(err, ERR_FILEINFO_ASSEMBLE_BLOCKS__ALREADY_EXISTS):
		// Do nothing: it just means we've assembled the file already
	case err != nil:
		return err
	}
	fileInfo.IsSeeding = true
	return nil
}
