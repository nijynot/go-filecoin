package mining

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/filecoin-project/go-filecoin/types"
	"github.com/stretchr/testify/assert"
)

func TestMineOnce(t *testing.T) {
	assert := assert.New(t)
	mockBg := &MockBlockGenerator{}
	baseBlock := &types.Block{StateRoot: types.SomeCid()}

	var mineCtx context.Context
	// Echoes the sent block to output.
	echoMine := func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		mineCtx = c
		outCh <- Output{NewBlock: b}
	}
	worker := NewWorkerWithMineAndWork(mockBg, echoMine, func() {})
	result := MineOnce(context.Background(), worker, baseBlock)
	assert.NoError(result.Err)
	assert.True(baseBlock.StateRoot.Equals(result.NewBlock.StateRoot))
	assert.Error(mineCtx.Err())
}

func TestMineEvery(t *testing.T) {
	assert := assert.New(t)
	mockBg := &MockBlockGenerator{}
	baseBlock := &types.Block{StateRoot: types.SomeCid()}
	ctx, cancel := context.WithCancel(context.Background())

	mineCnt := int32(0)
	// Counts number of times mine is called.
	countMine := func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		atomic.AddInt32(&mineCnt, 1)
	}
	worker := NewWorkerWithMineAndWork(mockBg, countMine, func() {})
	_, _, doneWg := MineEvery(ctx, worker, time.Millisecond, func() *types.Block { return baseBlock })
	time.Sleep(20 * time.Millisecond)
	cancel()
	doneWg.Wait()
	assert.True(mineCnt > 0)
	oldMineCnt := mineCnt
	time.Sleep(20 * time.Millisecond)
	assert.Equal(oldMineCnt, mineCnt)
}

func TestWorker_Start(t *testing.T) {
	assert := assert.New(t)
	newCid := types.NewCidForTestGetter()
	baseBlock := &types.Block{StateRoot: newCid()}
	mockBg := &MockBlockGenerator{}

	// Test that values are passed faithfully.
	ctx, cancel := context.WithCancel(context.Background())
	doSomeWorkCalled := false
	doSomeWork := func() { doSomeWorkCalled = true }
	mineCalled := false
	fakeMine := func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		mineCalled = true
		assert.NotEqual(ctx, c)
		assert.True(baseBlock.StateRoot.Equals(b.StateRoot))
		assert.Equal(mockBg, bg)
		doSomeWork()
		outCh <- Output{}
	}
	worker := NewWorkerWithMineAndWork(mockBg, fakeMine, doSomeWork)
	inCh, outCh, _ := worker.Start(ctx)
	inCh <- NewInput(context.Background(), baseBlock)
	<-outCh
	assert.True(mineCalled)
	assert.True(doSomeWorkCalled)
	cancel()

	// Test that we can push multiple blocks through.
	ctx, cancel = context.WithCancel(context.Background())
	fakeMine = func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		outCh <- Output{}
	}
	worker = NewWorkerWithMineAndWork(mockBg, fakeMine, func() {})
	inCh, outCh, _ = worker.Start(ctx)
	inCh <- NewInput(context.Background(), &types.Block{})
	inCh <- NewInput(context.Background(), &types.Block{})
	inCh <- NewInput(context.Background(), &types.Block{})
	<-outCh
	<-outCh
	<-outCh
	assert.Equal(ChannelEmpty, ReceiveOutCh(outCh))
	cancel() // Makes vet happy.

	// Test that it ignores blocks with lower score.
	ctx, cancel = context.WithCancel(context.Background())
	b1 := &types.Block{Height: 1}
	bWorseScore := &types.Block{Height: 0}
	fakeMine = func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		assert.True(b1.Cid().Equals(b.Cid()))
		outCh <- Output{}
	}
	worker = NewWorkerWithMineAndWork(mockBg, fakeMine, func() {})
	inCh, outCh, _ = worker.Start(ctx)
	inCh <- NewInput(context.Background(), b1)
	<-outCh
	inCh <- NewInput(context.Background(), bWorseScore)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(ChannelEmpty, ReceiveOutCh(outCh))
	cancel() // Makes vet happy.

	// Test that canceling the Input.Ctx cancels that input's mining run.
	miningCtx, miningCtxCancel := context.WithCancel(context.Background())
	inputCtx, inputCtxCancel := context.WithCancel(context.Background())
	var gotMineCtx context.Context
	fakeMine = func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		gotMineCtx = c
		outCh <- Output{}
	}
	worker = NewWorkerWithMineAndWork(mockBg, fakeMine, func() {})
	inCh, outCh, _ = worker.Start(miningCtx)
	inCh <- NewInput(inputCtx, &types.Block{})
	<-outCh
	inputCtxCancel()
	assert.Error(gotMineCtx.Err()) // Same context as miningRunCtx.
	assert.NoError(miningCtx.Err())
	miningCtxCancel() // Make vet happy.

	// Test that canceling the mining context stops mining, cancels
	// the inner context, and closes the output channel.
	miningCtx, miningCtxCancel = context.WithCancel(context.Background())
	inputCtx, inputCtxCancel = context.WithCancel(context.Background())
	gotMineCtx = context.Background()
	fakeMine = func(c context.Context, b *types.Block, bg BlockGenerator, doSomeWork DoSomeWorkFunc, outCh chan<- Output) {
		gotMineCtx = c
		outCh <- Output{}
	}
	worker = NewWorkerWithMineAndWork(mockBg, fakeMine, func() {})
	inCh, outCh, doneWg := worker.Start(miningCtx)
	inCh <- NewInput(inputCtx, &types.Block{})
	<-outCh
	miningCtxCancel()
	doneWg.Wait()
	assert.Equal(ChannelClosed, ReceiveOutCh(outCh))
	assert.Error(gotMineCtx.Err())
	inputCtxCancel() // Make vet happy.
}

func Test_mine(t *testing.T) {
	assert := assert.New(t)
	baseBlock := &types.Block{Height: 2}
	next := &types.Block{Height: 3}
	ctx := context.Background()

	// Success.
	mockBg := &MockBlockGenerator{}
	outCh := make(chan Output)
	mockBg.On("Generate", ctx, baseBlock).Return(next, nil)
	doSomeWorkCalled := false
	go Mine(ctx, baseBlock, mockBg, func() { doSomeWorkCalled = true }, outCh)
	r := <-outCh
	assert.NoError(r.Err)
	assert.True(doSomeWorkCalled)
	assert.True(r.NewBlock.Cid().Equals(next.Cid()))
	mockBg.AssertExpectations(t)

	// Block generation fails.
	mockBg = &MockBlockGenerator{}
	outCh = make(chan Output)
	mockBg.On("Generate", ctx, baseBlock).Return(nil, errors.New("boom"))
	doSomeWorkCalled = false
	go Mine(ctx, baseBlock, mockBg, func() { doSomeWorkCalled = true }, outCh)
	r = <-outCh
	assert.Error(r.Err)
	assert.False(doSomeWorkCalled)
	mockBg.AssertExpectations(t)
}
