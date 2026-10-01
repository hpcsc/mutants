package run

import (
	"bytes"
	"sort"
)

type lineOffsets []int

func newLineOffsets(source []byte) lineOffsets {
	offsets := lineOffsets{0}
	for offset := 0; ; {
		next := bytes.IndexByte(source[offset:], '\n')
		if next < 0 {
			return offsets
		}
		offset += next + 1
		offsets = append(offsets, offset)
	}
}

func (l lineOffsets) position(offset int) (line, column int) {
	index := sort.Search(len(l), func(i int) bool { return l[i] > offset }) - 1
	return index + 1, offset - l[index] + 1
}
