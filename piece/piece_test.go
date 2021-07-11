package piece

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_TryToReserve(t *testing.T) {
	// Setup
	// * Setup a bunch of pieces
	// * Setup a bunch of channels
	//   * One that should access the resource immediately
	//   * One that should access the resource after 1 hit
	//   * One that should access the resource after 2 hits
	//   * One that should not be able to access the resource since it exceeded 3 hits
	//
	// Action
	// * Each channel should run piece.SetStatus(whatever)
	//
	// Assertion
	// * Make sure the expected num of hits occur
	// for _, tc := range []struct {
	// }{} {
	// 	t.Run(tc.title, func(t *testing.T) {
	// 		actualBuff, actualErr := Foo(inputBuff)
	// 		require.ErrorIs(t, actualErr, tc.expectedErr)
	// 		require.Equal(t, tc.expectedBuff, actualBuff)
	// 	})
	// }

	// []struct {

	// }
	// type Test_SetStatus_FuncAndCondition struct {
	// }

	for _, tc := range []struct {
		title          string
		inputSleepTime []time.Duration
		inputPieceIdx  []int
		expectedOk     []bool
	}{
		{
			title:          "Good #1",
			inputSleepTime: []time.Duration{0, 0, 0},
			inputPieceIdx:  []int{1, 1, 2},
			// First channel:
			// * Engage mutex lock
			// * Reserve piece[1] and succeeds
			// * Disengage mutex lock
			//
			// Second channel:
			// * Waits until it can engage the mutex lock
			// * Engage mutex lock
			// * Tries to reserve piece[1] but fails
			// * Disengage mutex lock
			//
			// Third channel is working on a different piece
			expectedOk: []bool{true, false, true},
		},
		{
			title:          "Good #2",
			inputSleepTime: []time.Duration{0, 100, 1000},
			inputPieceIdx:  []int{1, 1, 1},
			expectedOk:     []bool{true, false, false},
		},
	} {
		t.Run(tc.title, func(t *testing.T) {
			var wg sync.WaitGroup
			inputSafePieces := NewSafePieces()
			for i := 0; i < 3; i++ {
				inputSafePieces.Put(NewPiece(i, 0x300, PIECESTATUS_IDLE))
			}

			for i := 0; i < len(tc.inputSleepTime); i++ {
				i2 := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					time.Sleep(tc.inputSleepTime[i2] * time.Millisecond)
					ok := inputSafePieces.TryToReserve(tc.inputPieceIdx[i2], "id")
					require.Equal(t, tc.expectedOk[i2], ok)
				}()
			}
			wg.Wait()
		})
	}
}
