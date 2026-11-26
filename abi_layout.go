package calldataverifier

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

const abiWordSize = 32

func abiHeadSize(t *abi.Type) (size int, dynamic bool) {
	switch t.T {
	case abi.BytesTy, abi.StringTy, abi.SliceTy:
		return abiWordSize, true
	case abi.ArrayTy:
		element, dynamic := abiHeadSize(t.Elem)
		if dynamic {
			return abiWordSize, true
		}
		return t.Size * element, false
	case abi.TupleTy:
		for _, field := range t.TupleElems {
			width, dynamic := abiHeadSize(field)
			if dynamic {
				return abiWordSize, true
			}
			size += width
		}
		return size, false
	default:
		return abiWordSize, false
	}
}

func abiSequence(data []byte, count int, field func(int) *abi.Type) (int, bool) {
	head := 0
	for i := 0; i < count; i++ {
		width, _ := abiHeadSize(field(i))
		if width > len(data)-head {
			return 0, false
		}
		head += width
	}
	tail := head
	for i, cursor := 0, 0; i < count; i++ {
		t := field(i)
		width, dynamic := abiHeadSize(t)
		if dynamic {
			offset, ok := abiIndex(data[cursor:])
			if !ok || offset != tail {
				return 0, false
			}
			size, ok := abiTail(data[tail:], t)
			if !ok {
				return 0, false
			}
			tail += size
		}
		cursor += width
	}
	return tail, true
}

func abiTail(data []byte, t *abi.Type) (int, bool) {
	switch t.T {
	case abi.BytesTy, abi.StringTy:
		length, ok := abiIndex(data)
		if !ok || length > len(data)-abiWordSize {
			return 0, false
		}
		payload := data[abiWordSize:]
		padding := (abiWordSize - length%abiWordSize) % abiWordSize
		if padding > len(payload)-length {
			return 0, false
		}
		return abiWordSize + length + padding, true
	case abi.SliceTy:
		count, ok := abiIndex(data)
		if !ok || count > (len(data)-abiWordSize)/abiWordSize {
			return 0, false
		}
		size, ok := abiSequence(data[abiWordSize:], count, func(int) *abi.Type { return t.Elem })
		return abiWordSize + size, ok
	case abi.ArrayTy:
		return abiSequence(data, t.Size, func(int) *abi.Type { return t.Elem })
	case abi.TupleTy:
		return abiSequence(data, len(t.TupleElems), func(i int) *abi.Type { return t.TupleElems[i] })
	default:
		return 0, false
	}
}

func abiIndex(data []byte) (int, bool) {
	var zero [24]byte
	if len(data) < abiWordSize || !bytes.Equal(data[:24], zero[:]) {
		return 0, false
	}
	n := binary.BigEndian.Uint64(data[24:abiWordSize])
	if n > math.MaxInt {
		return 0, false
	}
	return int(n), true
}
