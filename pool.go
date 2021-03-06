package pipeline

import (
	"context"
	"fmt"
	"sync"
)

type fixedPool struct {
	fifos []Stage
}

// FixedPool returns a Stage that spins up a pool containing numWorkers
// to process incoming data in parallel and emit their outputs to the next stage.
func FixedPool(task Task, num int) Stage {
	if num <= 0 {
		return nil
	}

	fifos := make([]Stage, num)
	for i := 0; i < num; i++ {
		fifos[i] = FIFO(task)
	}

	return &fixedPool{fifos: fifos}
}

// Run implements Stage.
func (p *fixedPool) Run(ctx context.Context, params StageParams) {
	var wg sync.WaitGroup

	// Spin up each task in the pool and wait for them to exit
	for i := 0; i < len(p.fifos); i++ {
		wg.Add(1)
		go func(idx int) {
			p.fifos[idx].Run(ctx, params)
			wg.Done()
		}(i)
	}

	wg.Wait()
}

type dynamicPool struct {
	task      Task
	tokenPool chan struct{}
}

// DynamicPool returns a Stage that maintains a dynamic pool that can scale
// up to max parallel tasks for processing incoming inputs in parallel and
// emitting their outputs to the next stage.
func DynamicPool(task Task, max int) Stage {
	if max <= 0 {
		return nil
	}

	tokenPool := make(chan struct{}, max)
	for i := 0; i < max; i++ {
		tokenPool <- struct{}{}
	}

	return &dynamicPool{task: task, tokenPool: tokenPool}
}

// Run implements Stage.
func (p *dynamicPool) Run(ctx context.Context, sp StageParams) {
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case dataIn, ok := <-sp.Input():
			if !ok {
				break loop
			}

			var token struct{}
			select {
			case token = <-p.tokenPool:
			case <-ctx.Done():
				break loop
			}

			go func(dataIn Data, token struct{}) {
				defer func() { p.tokenPool <- token }()
				dataOut, err := p.task.Process(ctx, dataIn)
				if err != nil {
					sp.Error().Append(fmt.Errorf("pipeline stage %d: %v", sp.Position(), err))
					return
				}

				// If the task did not output data for the
				// next stage there is nothing we need to do.
				if dataOut == nil {
					dataIn.MarkAsProcessed()
					return
				}

				// Output processed data
				select {
				case sp.Output() <- dataOut:
				case <-ctx.Done():
				}
			}(dataIn, token)
		}
	}

	// Wait for all workers to exit by trying to empty the token pool
	for i := 0; i < cap(p.tokenPool); i++ {
		<-p.tokenPool
	}
}
