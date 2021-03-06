package pipeline

import (
	"context"
	"sync"
)

type broadcast struct {
	fifos []Stage
}

// Broadcast returns a Stage that passes a copy of each incoming data
// to all specified tasks and emits their outputs to the next stage.
func Broadcast(tasks ...Task) Stage {
	if len(tasks) == 0 {
		return nil
	}

	fifos := make([]Stage, len(tasks))
	for i, t := range tasks {
		fifos[i] = FIFO(t)
	}

	return &broadcast{fifos: fifos}
}

// Run implements Stage.
func (b *broadcast) Run(ctx context.Context, sp StageParams) {
	var wg sync.WaitGroup
	var inCh = make([]chan Data, len(b.fifos))

	// Start each FIFO in a goroutine. Each FIFO gets its own dedicated
	// input channel and the shared output channel passed to Run.
	for i := 0; i < len(b.fifos); i++ {
		wg.Add(1)
		inCh[i] = make(chan Data)
		go func(fifoIndex int) {
			fifoParams := &params{
				stage:    sp.Position(),
				inCh:     inCh[fifoIndex],
				outCh:    sp.Output(),
				errQueue: sp.Error(),
			}
			b.fifos[fifoIndex].Run(ctx, fifoParams)
			wg.Done()
		}(i)
	}
loop:
	for {
		// Read incoming data and pass them to each FIFO
		select {
		case <-ctx.Done():
			break loop
		case data, ok := <-sp.Input():
			if !ok {
				break loop
			}

			for i := len(b.fifos) - 1; i >= 0; i-- {
				// As each FIFO might modify the data, to
				// avoid data races we need to make a copy
				// of the data for all FIFOs except the first.
				var fifoData = data
				if i != 0 {
					fifoData = data.Clone()
				}
				select {
				case <-ctx.Done():
					break loop
				case inCh[i] <- fifoData:
					// data sent to i_th FIFO
				}
			}
		}
	}

	// Close input channels and wait for FIFOs to exit
	for _, ch := range inCh {
		close(ch)
	}
	wg.Wait()
}
