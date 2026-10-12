package metrics

import (
	"math"
	"sort"
	"time"
)

func normalizedFloat(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func downsampleSamples(samples []Sample, maxPoints int) []Sample {
	if maxPoints <= 0 || len(samples) <= maxPoints {
		return samples
	}
	if maxPoints <= 1 {
		return []Sample{samples[len(samples)-1]}
	}
	if maxPoints == 2 {
		return []Sample{samples[0], samples[len(samples)-1]}
	}
	start := samples[0].Time.Unix()
	end := samples[len(samples)-1].Time.Unix()
	if end <= start {
		return downsampleSamplesByIndex(samples, maxPoints)
	}
	bucketSeconds := int64((end - start + int64(maxPoints) - 1) / int64(maxPoints))
	if bucketSeconds <= 0 {
		bucketSeconds = 1
	}
	indexes := make([]int, 0, maxPoints*2)
	addIndex := func(idx int) {
		if len(indexes) == 0 || indexes[len(indexes)-1] < idx {
			indexes = append(indexes, idx)
		}
	}
	addIndex(0)
	for startIndex := 0; startIndex < len(samples); {
		bucket := (samples[startIndex].Time.Unix() - start) / bucketSeconds
		endIndex := startIndex + 1
		for endIndex < len(samples) && (samples[endIndex].Time.Unix()-start)/bucketSeconds == bucket {
			endIndex++
		}
		rep := representativeSampleIndexes(samples, startIndex, endIndex)
		sort.Ints(rep)
		for _, idx := range rep {
			addIndex(idx)
		}
		startIndex = endIndex
	}
	addIndex(len(samples) - 1)
	result := make([]Sample, len(indexes))
	for i, index := range indexes {
		result[i] = samples[index]
	}
	if len(result) <= maxPoints {
		return result
	}
	return downsampleSamplesByIndex(result, maxPoints)
}

func representativeSampleIndexes(samples []Sample, startIndex int, endIndex int) []int {
	if endIndex-startIndex <= 1 {
		return []int{startIndex}
	}
	indexes := []int{startIndex, endIndex - 1}
	maxCPUIndex := startIndex
	maxNetworkIndex := startIndex
	maxDiskIndex := startIndex
	for i := startIndex; i < endIndex; i++ {
		if samples[i].CPUPercent > samples[maxCPUIndex].CPUPercent {
			maxCPUIndex = i
		}
		if samples[i].NetworkRxBPS+samples[i].NetworkTxBPS > samples[maxNetworkIndex].NetworkRxBPS+samples[maxNetworkIndex].NetworkTxBPS {
			maxNetworkIndex = i
		}
		if samples[i].DiskReadBPS+samples[i].DiskWriteBPS > samples[maxDiskIndex].DiskReadBPS+samples[maxDiskIndex].DiskWriteBPS {
			maxDiskIndex = i
		}
	}
	indexes = append(indexes, maxCPUIndex, maxNetworkIndex, maxDiskIndex)
	return indexes
}

func downsampleSamplesByIndex(samples []Sample, maxPoints int) []Sample {
	if len(samples) <= maxPoints {
		return samples
	}
	if maxPoints <= 1 {
		return []Sample{samples[len(samples)-1]}
	}
	step := float64(len(samples)-1) / float64(maxPoints-1)
	result := make([]Sample, 0, maxPoints)
	lastIndex := -1
	for i := range maxPoints {
		index := int(mathRound(float64(i) * step))
		if index <= lastIndex {
			index = lastIndex + 1
		}
		if index >= len(samples) {
			index = len(samples) - 1
		}
		result = append(result, samples[index])
		lastIndex = index
	}
	return result
}

func beginningOfLocalDay(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

func mathRound(value float64) int64 {
	if value < 0 {
		return int64(value - 0.5)
	}
	return int64(value + 0.5)
}

func bytesPerSecond(current uint64, previous uint64, seconds float64) uint64 {
	if current < previous || seconds <= 0 {
		return 0
	}
	return uint64(float64(current-previous) / seconds)
}

func maxUint64(a uint64, b uint64) uint64 {
	if b > a {
		return b
	}
	return a
}

func maxFloat64(a float64, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
