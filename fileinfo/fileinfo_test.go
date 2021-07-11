package fileinfo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/afjoseph/commongo/print"
	commongoutil "github.com/afjoseph/commongo/util"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/projectpath"
	"github.com/afjoseph/totorrent/util"
	"github.com/stretchr/testify/require"
)

func Test_NewFileInfo(t *testing.T) {
	for _, tc := range []struct {
		title           string
		torrentLength   int64
		totalPieces     int
		lastPieceLength int64
		pieceLength     int64
		blockLength     uint32
		lastBlockLength uint32
		blockCount      int
	}{
		{
			title:           "Good #1",
			torrentLength:   113, // Chosen randomly
			totalPieces:     4,   // 3+1(last piece): Chosen randomly
			pieceLength:     37,  // 113//3 = 37
			lastPieceLength: 2,   // 113%3  = 2
			blockLength:     5,   // Chosen randomly
			blockCount:      8,   // 37//5 = 7 + 1(last block)
			lastBlockLength: 2,   // 37%5  = 2
		},
		{
			title:           "Good #2",
			torrentLength:   100, // Chosen randomly
			totalPieces:     5,   // 3+1(last piece): Chosen randomly
			pieceLength:     20,  // 113//3 = 37
			lastPieceLength: 20,  // 113%3  = 2
			blockLength:     4,   // Chosen randomly
			blockCount:      5,   // 37//5 = 7 + 1(last block)
			lastBlockLength: 4,   // 37%5  = 2
		},
		// TODO include some cases where bad things happen
	} {
		t.Run(tc.title, func(t *testing.T) {
			fileInfo, err := NewFileInfo(
				tc.totalPieces, tc.pieceLength,
				int(tc.blockLength), "", "",
				&metainfo.Info{Length: tc.torrentLength},
				DefaultFinalizeFunc,
				false,
			)
			require.NoError(t, err)
			// Assert fileInfo.Pieces is not nil
			require.NotNil(t, fileInfo.Pieces)
			// Assert fileInfo.Pieces were distributed successfully
			require.Equal(t, tc.totalPieces, fileInfo.Pieces.Len())
			// Assert total sum of all pieces is equal to torrent length
			actualtorrentLength := int64(0)
			for _, p := range fileInfo.Pieces.Data() {
				actualtorrentLength += p.Len()
			}
			require.Equal(t, tc.torrentLength, actualtorrentLength)

			// Assert pieceLength is expected
			for _, p := range fileInfo.Pieces.Data() {
				if p.LastPiece() {
					require.Equal(t, tc.lastPieceLength, p.Len())
				} else {
					require.Equal(t, tc.pieceLength, p.Len())
				}
				// Assert blockLength is expected
				for _, b := range p.Blocks() {
					if b.IsLastBlock {
						require.Equal(t, tc.lastBlockLength, b.Length)
					} else {
						require.Equal(t, tc.blockLength, b.Length)
					}
				}
			}

		})
	}
}

// Test_Finalize_singlefilemode downloads some blocks into files by calling SaveBlock() and
// tests whether they all assemble to one, big file
func Test_Finalize__singlefilemode(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// -----
	// - Init some random pieces
	inputPieces := piece.NewRandomPieces(
		util.RandRange(1, 20),        // pieceCount
		int64(util.RandRange(1, 20)), // pieceLength
		0xff,                         // blockLength
	)
	// - Init FileInfo
	inputFileInfo := &FileInfo{
		DownloadedBlocksDir: util.MkdirTempOrPanic("", "totorrenttmp_"),
		FinalDownloadDir:    util.MkdirTempOrPanic("", "totorrenttmp_"),
		torrentInfo:         &metainfo.Info{Name: "bunnyfoofoo"},
		Pieces:              inputPieces,
		FinalizeFunc:        DefaultFinalizeFunc,
	}
	defer inputFileInfo.DeleteBlocksDir()
	defer inputFileInfo.DeleteFinalDownloadDir()
	// - Save all blocks to the filesystem
	for _, b := range inputPieces.AllBlocks() {
		err := inputFileInfo.SaveBlock(b)
		require.NoError(t, err)
	}
	// - Early assert: require that num of files in blocks dir is equal to
	// len(inputBlocks)
	dirEntry, err := os.ReadDir(inputFileInfo.DownloadedBlocksDir)
	require.NoError(t, err)
	require.Equal(t,
		inputPieces.TotalBlocksInAllPiecesCount(),
		len(dirEntry))

	// Action
	// ------
	err = inputFileInfo.FinalizeFunc(inputFileInfo)
	require.NoError(t, err)

	// Assertions
	// ----------
	// - Require that inputFileInfo is in 'seeding' mode
	require.Equal(t, true, inputFileInfo.IsSeeding)
	// - Call GetBlock() twice: one with seeding and one without:
	//   - If IsSeeding==false, GetBlock() will get the block from
	//   inputFileInfo.Pieces, which has the full block, so the test should
	//   succeed.
	//   - If IsSeeding==true, GetBlock() will query the filesystem to get the
	//   block's buff, so the test should succeed as well.
	// XXX I do realize it is weird that we're running the test in two
	// conditions (that are not injected in the function's declaration).
	// Testing like this, however, assures us that GetBlock() is fully covered
	// in both scenarios: with and without seeding.
	for _, b := range inputPieces.AllBlocks() {
		actualBlock, err := inputFileInfo.GetBlock(
			b.PieceIdx, b.Begin, b.Length, false)
		require.NoError(t, err)
		require.Equal(t, b, actualBlock)

		actualBlock, err = inputFileInfo.GetBlock(
			b.PieceIdx, b.Begin, b.Length, true)
		require.NoError(t, err)
		require.Equal(t, b, actualBlock)
	}
}

// TODO this test's name is misleading: we're testing a lot of functions, not
// just Finalize()
// Test_Finalize__from_torrent does the following:
// - Parses a torrent file, made from a real test file we have
// - Makes a FileInfo out of the information we get from the parsed torrent file
// - Loops over all blocks in all pieces in FileInfo
//   - and save each block's bytes to a temporary file
// - Asserts that the temporary file is equal to the real test file we made the
// torrent file from
//
// XXX This test **might** be not testing a real scenario since we're entering
// 'seeding' mode in our FileInfo and, when we do that, FileInfo.GetBlock()
// will fetch the bytes directly from the test file. In other words, we're
// testing the same bytes using the same bytes. This is still a useful test
// since it tests the basic functionality of NewfileInfo()'s piece creation
// stategy against a real torrent file, as well as testing FileInfo.GetBlock()
func Test_Finalize__from_torrent_singlefilemode(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	// Setup
	// -----
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
	inputFileInfo, err := NewFileInfo(
		// We always divide by 20 (length of a sha1 hash) when parsing torrent
		// files
		len(torrentInfo.Pieces)/20,
		torrentInfo.PieceLength,
		piece.BLOCK_LENGTH,
		inputFinalDownloadDirPath,
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		&torrentInfo,
		DefaultFinalizeFunc,
		false,
	)
	require.NoError(t, err)
	// - Open a file descriptor to a tmp file so we can write the blocks to it
	tmpDir := util.MkdirTempOrPanic("", "totorrenttesttmp_")
	defer os.RemoveAll(tmpDir)
	tmpFinalDownloadFilePath := filepath.Join(tmpDir, "test")
	tmpFinalDownloadFile, err := os.Create(tmpFinalDownloadFilePath)
	require.NoError(t, err)
	defer tmpFinalDownloadFile.Close()

	// Action
	// ------
	// - Finalize the inputFileInfo
	err = inputFileInfo.FinalizeFunc(inputFileInfo)
	require.NoError(t, err)
	require.Equal(t, true, inputFileInfo.IsSeeding)
	// - Write all blocks, in sequential order, to a temporary file
	for _, b := range inputFileInfo.Pieces.AllBlocks() {
		actualBlock, err := inputFileInfo.GetBlock(
			b.PieceIdx, b.Begin, b.Length, true)
		require.NoError(t, err)
		require.Equal(t, b, actualBlock)
		_, err = tmpFinalDownloadFile.Write(actualBlock.Buff)
		require.NoError(t, err)
	}

	// Assertions
	// ----------
	// - Assert that the temporary file we wrote is the same as the one we have
	// in test_assets
	_, _, actualExitCode, err := commongoutil.Exec("", "cmp -s %s %s",
		inputFinalDownloadFilepath, tmpFinalDownloadFilePath)
	require.NoError(t, err)
	require.Equal(t, 0, actualExitCode)
}
