// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package proposer

import (
	"fmt"
	"sort"
	"time"

	"github.com/flare-foundation/flare/ids"
	"github.com/flare-foundation/flare/snow/validators"
	"github.com/flare-foundation/flare/utils/math"
	"github.com/flare-foundation/flare/utils/sampler"
	"github.com/flare-foundation/flare/utils/wrappers"
)

// Proposer list constants
const (
	MaxWindows     = 6
	WindowDuration = 5 * time.Second
	MaxDelay       = MaxWindows * WindowDuration
)

var _ Windower = &windower{}

type Windower interface {
	Delay(
		chainHeight uint64,
		validatorID ids.ShortID,
		parentID ids.ID,
	) (time.Duration, error)
}

// windower interfaces with P-Chain and it is responsible for calculating the
// delay for the block submission window of a given validator
type windower struct {
	validators  validators.Manager
	subnetID    ids.ID
	chainSource uint64
	sampler     sampler.WeightedWithoutReplacement
}

func New(validators validators.Manager, subnetID, chainID ids.ID) Windower {
	w := wrappers.Packer{Bytes: chainID[:]}
	return &windower{
		validators:  validators,
		subnetID:    subnetID,
		chainSource: w.UnpackLong(),
		sampler:     sampler.NewDeterministicWeightedWithoutReplacement(),
	}
}

func (w *windower) Delay(chainHeight uint64, validatorID ids.ShortID, parentID ids.ID) (time.Duration, error) {
	if validatorID == ids.ShortEmpty {
		return MaxDelay, nil
	}

	// get the validator set by the p-chain height
	validatorSet, err := w.validators.GetValidatorsByBlockID(parentID)
	if err != nil {
		return 0, fmt.Errorf("could not get validators (block: %x): %w", parentID, err)
	}

	// convert the list of validators to a slice
	validators := validatorSet.List()
	weight := uint64(0)
	for _, validator := range validators {
		weight, err = math.Add64(weight, validator.Weight())
		if err != nil {
			return 0, err
		}
	}

	// canonically sort validators
	// Note: validators are sorted by ID, sorting by weight would not create a
	// canonically sorted list
	sort.Sort(validatorsSlice(validators))

	// convert the slice of validators to a slice of weights
	validatorWeights := make([]uint64, len(validators))
	for i, validator := range validators {
		validatorWeights[i] = validator.Weight()
	}

	if err := w.sampler.Initialize(validatorWeights); err != nil {
		return 0, err
	}

	numToSample := MaxWindows
	if weight < uint64(numToSample) {
		numToSample = int(weight)
	}

	seed := chainHeight ^ w.chainSource
	w.sampler.Seed(int64(seed))

	indices, err := w.sampler.Sample(numToSample)
	if err != nil {
		return 0, err
	}

	delay := time.Duration(0)
	for _, index := range indices {
		nodeID := validators[index].ID()
		if nodeID == validatorID {
			return delay, nil
		}
		delay += WindowDuration
	}
	return delay, nil
}
