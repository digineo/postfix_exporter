package main

import (
	"os"
	"testing"

	"github.com/kumina/postfix_exporter/mock"
	"github.com/stretchr/testify/assert"
)

func TestCollectShowqFromReader(t *testing.T) {
	tests := []struct {
		name               string
		file               string
		wantErr            bool
		expectedTotalCount float64
	}{{
		name:               "basic test",
		file:               "testdata/showq.txt",
		wantErr:            false,
		expectedTotalCount: 118702,
	}}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.Open(tt.file)
			if err != nil {
				t.Error(err)
			}

			sizeHistogram := mock.NewHistogramVecMock()
			ageHistogram := mock.NewHistogramVecMock()
			if err := CollectTextualShowqFromScanner(sizeHistogram, ageHistogram, file, "postfix"); (err != nil) != tt.wantErr {
				t.Errorf("CollectShowqFromReader() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.expectedTotalCount, sizeHistogram.GetSum(), "Expected a lot more data.")
			assert.Less(t, 0.0, ageHistogram.GetSum(), "Age not greater than 0")
		})
	}
}
