package main

import (
	"os"
	"testing"

	"github.com/kumina/postfix_exporter/mock"
	"github.com/stretchr/testify/assert"
)

func TestCollectShowqFromReader(t *testing.T) {
	t.Parallel()

	const expectedTotalCount = float64(118702)
	const expectedMaxAge = 0.0

	file, err := os.Open("testdata/showq.txt")
	if err != nil {
		t.Error(err)
	}

	sizeHistogram := mock.NewHistogramVecMock()
	ageHistogram := mock.NewHistogramVecMock()
	if err := CollectTextualShowqFromScanner(sizeHistogram, ageHistogram, file, "postfix"); err != nil {
		t.Errorf("CollectShowqFromReader() error = %v", err)
	}
	assert.Equal(t, expectedTotalCount, sizeHistogram.GetSum(), "Expected a lot more data.")
	assert.Less(t, expectedMaxAge, ageHistogram.GetSum(), "Age not greater than 0")
}
