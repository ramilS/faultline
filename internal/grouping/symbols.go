package grouping

import "slices"

// SymbolLookup returns one name per address, "" where it has none, and may
// return names alongside an error.
type SymbolLookup func(addresses []uint64) ([]string, error)

func WithSymbols(input GroupingInput, lookup SymbolLookup) (GroupingInput, error) {
	if input.Exception == nil {
		return input, nil
	}
	var targets []int
	var addresses []uint64
	for _, index := range groupingFrameIndices(input.Exception.Frames) {
		frame := input.Exception.Frames[index]
		if frame.InstructionAddr == nil || normalizeFrame(frame.Module, frame.Function) != "" {
			continue
		}
		targets = append(targets, index)
		addresses = append(addresses, *frame.InstructionAddr)
	}
	if len(targets) == 0 {
		return input, nil
	}
	names, err := lookup(addresses)
	exception := *input.Exception
	exception.Frames = slices.Clone(exception.Frames)
	for position, index := range targets {
		if position < len(names) {
			exception.Frames[index].Symbol = names[position]
		}
	}
	input.Exception = &exception
	return input, err
}
