package util

import (
	"path/filepath"
	"testing"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/projectpath"
	"github.com/stretchr/testify/require"
)

func Test_DecodeTorrentFile(t *testing.T) {
	print.SetLevel(print.LOG_DEBUG)

	myMetainfo, err := DecodeTorrentFile(filepath.Join(projectpath.Root, "test_assets/multi_file.torrent"))
	require.NoError(t, err)
	require.NotNil(t, myMetainfo)
	myInfo, err := myMetainfo.UnmarshalInfo()
	require.NoError(t, err)
	print.Infof("%+v\n", len(myInfo.Pieces)/20)
}

func Test_IntSliceUniqueMergeSlices(t *testing.T) {
	for _, tc := range []struct {
		title               string
		inputSliceA         []int
		inputSliceB         []int
		expectedMergedSlice []int
	}{
		{
			title:               "Good 1",
			inputSliceA:         []int{1, 2, 3, 4},
			inputSliceB:         []int{4, 5, 6, 7},
			expectedMergedSlice: []int{1, 2, 3, 4, 5, 6, 7},
		},
		{
			title:               "Good 2",
			inputSliceA:         []int{1, 2, 3, 4},
			inputSliceB:         []int{1, 2, 3, 4},
			expectedMergedSlice: []int{1, 2, 3, 4},
		},
		{
			title:               "Good 3",
			inputSliceA:         []int{1, 2, 3, 4},
			inputSliceB:         []int{10, 20, 30, 40},
			expectedMergedSlice: []int{1, 2, 3, 4, 10, 20, 30, 40},
		},
		{
			title:               "Empty slice",
			inputSliceA:         []int{},
			inputSliceB:         []int{1, 2, 3, 4},
			expectedMergedSlice: []int{1, 2, 3, 4},
		},
		{
			title:               "nil slice",
			inputSliceA:         nil,
			inputSliceB:         []int{1, 2, 3, 4},
			expectedMergedSlice: []int{1, 2, 3, 4},
		},
	} {
		t.Run(tc.title, func(t *testing.T) {
			require.Equal(t,
				tc.expectedMergedSlice,
				IntSliceUniqueMergeSlices(tc.inputSliceA, tc.inputSliceB))
		})
	}
}
