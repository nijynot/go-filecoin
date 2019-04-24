package paymentbroker

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-hamt-ipld"
	cbor "github.com/ipfs/go-ipld-cbor"

	"github.com/filecoin-project/go-filecoin/abi"
	"github.com/filecoin-project/go-filecoin/actor"
	"github.com/filecoin-project/go-filecoin/address"
	"github.com/filecoin-project/go-filecoin/exec"
	"github.com/filecoin-project/go-filecoin/types"
	"github.com/filecoin-project/go-filecoin/vm/errors"
)

const (
	// ErrNonAccountActor indicates an non-account actor attempted to create a payment channel.
	ErrNonAccountActor = 33
	// ErrDuplicateChannel indicates an attempt to create a payment channel with an existing id.
	ErrDuplicateChannel = 34
	// ErrEolTooLow indicates an attempt to lower the Eol of a payment channel.
	ErrEolTooLow = 35
	// ErrReclaimBeforeEol indicates an attempt to reclaim funds before the eol of the channel.
	ErrReclaimBeforeEol = 36
	// ErrInsufficientChannelFunds indicates an attempt to take more funds than the channel contains.
	ErrInsufficientChannelFunds = 37
	// ErrUnknownChannel indicates an invalid channel id.
	ErrUnknownChannel = 38
	// ErrWrongTarget indicates attempt to redeem from wrong target account.
	ErrWrongTarget = 39
	// ErrExpired indicates the block height has exceeded the eol.
	ErrExpired = 40
	// ErrAlreadyWithdrawn indicates amount of the voucher has already been withdrawn.
	ErrAlreadyWithdrawn = 41
	// ErrInvalidSignature indicates the signature is invalid.
	ErrInvalidSignature = 42
	//ErrTooEarly indicates that the block height is too low to satisfy a voucher
	ErrTooEarly = 43
	//ErrConditionInvalid indicates that the condition attached to a voucher did not execute successfully
	ErrConditionInvalid = 43
)

// CancelDelayBlockTime is the number of rounds given to the target to respond after the channel
// is canceled before it expires.
// TODO: what is a secure value for this?  Value is arbitrary right now.
// See https://github.com/filecoin-project/go-filecoin/issues/1887
const CancelDelayBlockTime = 10000

// Errors map error codes to revert errors this actor may return.
var Errors = map[uint8]error{
	ErrTooEarly:                 errors.NewCodedRevertError(ErrTooEarly, "block height too low to redeem voucher"),
	ErrNonAccountActor:          errors.NewCodedRevertError(ErrNonAccountActor, "Only account actors may create payment channels"),
	ErrDuplicateChannel:         errors.NewCodedRevertError(ErrDuplicateChannel, "Duplicate create channel attempt"),
	ErrEolTooLow:                errors.NewCodedRevertError(ErrEolTooLow, "payment channel eol may not be decreased"),
	ErrReclaimBeforeEol:         errors.NewCodedRevertError(ErrReclaimBeforeEol, "payment channel may not reclaimed before eol"),
	ErrInsufficientChannelFunds: errors.NewCodedRevertError(ErrInsufficientChannelFunds, "voucher amount exceeds amount in channel"),
	ErrUnknownChannel:           errors.NewCodedRevertError(ErrUnknownChannel, "payment channel is unknown"),
	ErrWrongTarget:              errors.NewCodedRevertError(ErrWrongTarget, "attempt to redeem channel from wrong target account"),
	ErrExpired:                  errors.NewCodedRevertError(ErrExpired, "block height has exceeded channel's end of life"),
	ErrAlreadyWithdrawn:         errors.NewCodedRevertError(ErrAlreadyWithdrawn, "update amount has already been redeemed"),
	ErrInvalidSignature:         errors.NewCodedRevertErrorf(ErrInvalidSignature, "signature failed to validate"),
}

func init() {
	cbor.RegisterCborType(PaymentChannel{})
}

// PaymentChannel records the intent to pay funds to a target account.
type PaymentChannel struct {
	// Target is the address of the account to which funds will be transferred
	Target address.Address `json:"target"`

	// Amount is the total amount of FIL that has been transferred to the channel from the payer
	Amount *types.AttoFIL `json:"amount"`

	// AmountRedeemed is the amount of FIL already transferred to the target
	AmountRedeemed *types.AttoFIL `json:"amount_redeemed"`

	// AgreedEol is the expiration for the payment channel agreed upon by the
	// payer and payee upon initialization or extension
	AgreedEol *types.BlockHeight `json:"agreed_eol"`

	// Eol is the actual expiration for the payment channel which can differ from
	// AgreedEol when the payment channel is in dispute
	Eol *types.BlockHeight `json:"eol"`
}

// Actor provides a mechanism for off chain payments.
// It allows the creation of payment channels that hold funds for a target account
// and permits that account to withdraw funds only with a voucher signed by the
// channel's creator.
type Actor struct{}

// InitializeState stores the actor's initial data structure.
func (pb *Actor) InitializeState(storage exec.Storage, initializerData interface{}) error {
	// pb's default state is an empty lookup, so this method is a no-op
	return nil
}

// Exports returns the actor's exports.
func (pb *Actor) Exports() exec.Exports {
	return paymentBrokerExports
}

var _ exec.ExecutableActor = (*Actor)(nil)

var paymentBrokerExports = exec.Exports{
	"cancel": &exec.FunctionSignature{
		Params: []abi.Type{abi.ChannelID},
		Return: nil,
	},
	"close": &exec.FunctionSignature{
		Params: []abi.Type{abi.Address, abi.ChannelID, abi.AttoFIL, abi.BlockHeight, abi.Predicate, abi.Bytes, abi.Parameters},
		Return: nil,
	},
	"createChannel": &exec.FunctionSignature{
		Params: []abi.Type{abi.Address, abi.BlockHeight},
		Return: []abi.Type{abi.ChannelID},
	},
	"extend": &exec.FunctionSignature{
		Params: []abi.Type{abi.ChannelID, abi.BlockHeight},
		Return: nil,
	},
	"ls": &exec.FunctionSignature{
		Params: []abi.Type{abi.Address},
		Return: []abi.Type{abi.Bytes},
	},
	"reclaim": &exec.FunctionSignature{
		Params: []abi.Type{abi.ChannelID},
		Return: nil,
	},
	"redeem": &exec.FunctionSignature{
		Params: []abi.Type{abi.Address, abi.ChannelID, abi.AttoFIL, abi.BlockHeight, abi.Predicate, abi.Bytes, abi.Parameters},
		Return: nil,
	},
	"voucher": &exec.FunctionSignature{
		Params: []abi.Type{abi.ChannelID, abi.AttoFIL, abi.BlockHeight, abi.Predicate},
		Return: []abi.Type{abi.Bytes},
	},
}

// CreateChannel creates a new payment channel from the caller to the target.
// The value attached to the invocation is used as the deposit, and the channel
// will expire and return all of its money to the owner after the given block height.
func (pb *Actor) CreateChannel(vmctx exec.VMContext, target address.Address, eol *types.BlockHeight) (*types.ChannelID, uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return nil, exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	// require that from account be an account actor to ensure nonce is a valid id
	if !vmctx.IsFromAccountActor() {
		return nil, errors.CodeError(Errors[ErrNonAccountActor]), Errors[ErrNonAccountActor]
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	payerAddress := vmctx.Message().From
	channelID := types.NewChannelID(uint64(vmctx.Message().Nonce))

	err := withPayerChannels(ctx, storage, payerAddress, func(byChannelID exec.Lookup) error {
		// check to see if payment channel is duplicate
		_, err := byChannelID.Find(ctx, channelID.KeyString())
		if err != hamt.ErrNotFound { // we expect to not find the payment channel
			if err == nil {
				return Errors[ErrDuplicateChannel]
			}
			return errors.FaultErrorWrapf(err, "Error retrieving payment channel")
		}

		// add payment channel and commit
		err = byChannelID.Set(ctx, channelID.KeyString(), &PaymentChannel{
			Target:         target,
			Amount:         vmctx.Message().Value,
			AmountRedeemed: types.NewAttoFILFromFIL(0),
			AgreedEol:      eol,
			Eol:            eol,
		})
		if err != nil {
			return errors.FaultErrorWrap(err, "Could not set payment channel")
		}

		return nil
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return nil, 1, errors.FaultErrorWrap(err, "Error creating payment channel")
		}
		return nil, errors.CodeError(err), err
	}

	return channelID, 0, nil
}

// Redeem is called by the target account to withdraw funds with authorization from the payer.
// This method is exactly like Close except it doesn't close the channel.
// This is useful when you want to checkpoint the value in a payment, but continue to use the
// channel afterwards. The amt represents the total funds authorized so far, so that subsequent
// calls to Update will only transfer the difference between the given amt and the greatest
// amt taken so far. A series of channel transactions might look like this:
//                                Payer: 2000, Target: 0, Channel: 0
// payer createChannel(1000)   -> Payer: 1000, Target: 0, Channel: 1000
// target Redeem(100)          -> Payer: 1000, Target: 100, Channel: 900
// target Redeem(200)          -> Payer: 1000, Target: 200, Channel: 800
// target Close(500)           -> Payer: 1500, Target: 500, Channel: 0
//
// If a condition is provided in the voucher, concatenate its params with supplied params to send a message.
// Any non-fault error is considered a validation failure.
func (pb *Actor) Redeem(vmctx exec.VMContext, payer address.Address, chid *types.ChannelID, amt *types.AttoFIL, validAt *types.BlockHeight, condition *types.Predicate, sig []byte, redeemerSuppliedParams []interface{}) (uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	if !VerifyVoucherSignature(payer, chid, amt, validAt, condition, sig) {
		return errors.CodeError(Errors[ErrInvalidSignature]), Errors[ErrInvalidSignature]
	}

	if errCode, err := checkCondition(vmctx, condition, redeemerSuppliedParams); err != nil {
		return errCode, err
	}

	ctx := context.Background()
	storage := vmctx.Storage()

	err := withPayerChannels(ctx, storage, payer, func(byChannelID exec.Lookup) error {
		var channel *PaymentChannel

		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		// validate the amount can be sent to the target and send payment to that address.
		err = updateChannel(vmctx, vmctx.Message().From, channel, amt, validAt)
		if err != nil {
			return err
		}

		return byChannelID.Set(ctx, chid.KeyString(), channel)
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return 1, errors.FaultErrorWrap(err, "Error redeeming payment channel")
		}
		return errors.CodeError(err), err
	}

	return 0, nil
}

// Close first executes the logic performed in the the Update method, then returns all
// funds remaining in the channel to the payer account and deletes the channel.
func (pb *Actor) Close(vmctx exec.VMContext, payer address.Address, chid *types.ChannelID, amt *types.AttoFIL, validAt *types.BlockHeight, condition *types.Predicate, sig []byte, redeemerSuppliedParams []interface{}) (uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	if !VerifyVoucherSignature(payer, chid, amt, validAt, condition, sig) {
		return errors.CodeError(Errors[ErrInvalidSignature]), Errors[ErrInvalidSignature]
	}

	if errCode, err := checkCondition(vmctx, condition, redeemerSuppliedParams); err != nil {
		return errCode, err
	}

	ctx := context.Background()
	storage := vmctx.Storage()

	err := withPayerChannels(ctx, storage, payer, func(byChannelID exec.Lookup) error {
		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		// validate the amount can be sent to the target and send payment to that address.
		err = updateChannel(vmctx, vmctx.Message().From, channel, amt, validAt)
		if err != nil {
			return err
		}

		err = byChannelID.Set(ctx, chid.KeyString(), channel)
		if err != nil {
			return err
		}

		// return funds to payer
		return reclaim(ctx, vmctx, byChannelID, payer, chid, channel)
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return 1, errors.FaultErrorWrap(err, "Error updating or reclaiming channel")
		}
		return errors.CodeError(err), err
	}

	return 0, nil
}

// Extend can be used by the owner of a channel to add more funds to it and
// extend the Channel's lifespan.
func (pb *Actor) Extend(vmctx exec.VMContext, chid *types.ChannelID, eol *types.BlockHeight) (uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	payerAddress := vmctx.Message().From

	err := withPayerChannels(ctx, storage, payerAddress, func(byChannelID exec.Lookup) error {
		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		// eol can only be increased
		if channel.Eol.GreaterThan(eol) {
			return Errors[ErrEolTooLow]
		}

		// set new eol
		channel.AgreedEol = eol
		channel.Eol = eol

		// increment the value
		channel.Amount = channel.Amount.Add(vmctx.Message().Value)

		return byChannelID.Set(ctx, chid.KeyString(), channel)
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return 1, errors.FaultErrorWrap(err, "Error extending channel")
		}
		return errors.CodeError(err), err
	}

	return 0, nil
}

// Cancel can be used to end an off chain payment early. It lowers the EOL of
// the payment channel to 1 blocktime from now and allows a caller to reclaim
// their payments. In the time before the channel is closed, a target can
// potentially dispute a closer.
func (pb *Actor) Cancel(vmctx exec.VMContext, chid *types.ChannelID) (uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	payerAddress := vmctx.Message().From

	err := withPayerChannels(ctx, storage, payerAddress, func(byChannelID exec.Lookup) error {
		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		eol := vmctx.BlockHeight().Add(types.NewBlockHeight(CancelDelayBlockTime))

		// eol can only be decreased
		if channel.Eol.GreaterThan(eol) {
			channel.Eol = eol
		}

		return byChannelID.Set(ctx, chid.KeyString(), channel)
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return 1, errors.FaultErrorWrap(err, "Error cancelling channel")
		}
		return errors.CodeError(err), err
	}

	return 0, nil
}

// Reclaim is used by the owner of a channel to reclaim unspent funds in timed
// out payment Channels they own.
func (pb *Actor) Reclaim(vmctx exec.VMContext, chid *types.ChannelID) (uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	payerAddress := vmctx.Message().From

	err := withPayerChannels(ctx, storage, payerAddress, func(byChannelID exec.Lookup) error {
		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		// reclaim may only be called at or after Eol
		if vmctx.BlockHeight().LessThan(channel.Eol) {
			return Errors[ErrReclaimBeforeEol]
		}

		// return funds to payer
		return reclaim(ctx, vmctx, byChannelID, payerAddress, chid, channel)
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return 1, errors.FaultErrorWrap(err, "Error reclaiming channel")
		}
		return errors.CodeError(err), err
	}

	return 0, nil
}

// Voucher takes a channel id and amount creates a new unsigned PaymentVoucher
// against the given channel.  It also takes a block height parameter "validAt"
// enforcing that the voucher is not reclaimed until the given block height
// Voucher errors if the channel doesn't exist or contains less than request
// amount.
// If a condition is provided, attempts to redeem or close with the voucher will
// first send a message based on the condition and require a successful response
// for funds to be transferred.
func (pb *Actor) Voucher(vmctx exec.VMContext, chid *types.ChannelID, amount *types.AttoFIL, validAt *types.BlockHeight, condition *types.Predicate) ([]byte, uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return []byte{}, exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	payerAddress := vmctx.Message().From
	var voucher types.PaymentVoucher

	err := withPayerChannelsForReading(ctx, storage, payerAddress, func(byChannelID exec.Lookup) error {
		var channel *PaymentChannel

		chInt, err := byChannelID.Find(ctx, chid.KeyString())
		if err != nil {
			if err == hamt.ErrNotFound {
				return Errors[ErrUnknownChannel]
			}
			return errors.FaultErrorWrapf(err, "Could not retrieve payment channel with ID: %s", chid)
		}

		channel, ok := chInt.(*PaymentChannel)
		if !ok {
			return errors.NewFaultError("Expected PaymentChannel from channels lookup")
		}

		// voucher must be for less than total amount in channel
		if channel.Amount.LessThan(amount) {
			return Errors[ErrInsufficientChannelFunds]
		}

		// set voucher
		voucher = types.PaymentVoucher{
			Channel:   *chid,
			Payer:     vmctx.Message().From,
			Target:    channel.Target,
			Amount:    *amount,
			ValidAt:   *validAt,
			Condition: condition,
		}

		return nil
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return nil, 1, errors.FaultErrorWrap(err, "Error reclaiming channel")
		}
		return nil, errors.CodeError(err), err
	}

	voucherBytes, err := actor.MarshalStorage(voucher)
	if err != nil {
		return nil, 1, errors.FaultErrorWrap(err, "Error marshalling voucher")
	}

	return voucherBytes, 0, nil
}

// Ls returns all payment channels for a given payer address.
// The slice of channels will be returned as cbor encoded map from string channelId to PaymentChannel.
func (pb *Actor) Ls(vmctx exec.VMContext, payer address.Address) ([]byte, uint8, error) {
	if err := vmctx.Charge(actor.DefaultGasCost); err != nil {
		return []byte{}, exec.ErrInsufficientGas, errors.RevertErrorWrap(err, "Insufficient gas")
	}

	ctx := context.Background()
	storage := vmctx.Storage()
	channels := map[string]*PaymentChannel{}

	err := withPayerChannelsForReading(ctx, storage, payer, func(byChannelID exec.Lookup) error {
		kvs, err := byChannelID.Values(ctx)
		if err != nil {
			return err
		}

		for _, kv := range kvs {
			pc, ok := kv.Value.(*PaymentChannel)
			if !ok {
				return errors.NewFaultError("Expected PaymentChannel from channel lookup")
			}
			channels[kv.Key] = pc
		}

		return nil
	})

	if err != nil {
		// ensure error is properly wrapped
		if !errors.IsFault(err) && !errors.ShouldRevert(err) {
			return nil, 1, errors.FaultErrorWrap(err, "Error reclaiming channel")
		}
		return nil, errors.CodeError(err), err
	}

	channelsBytes, err := actor.MarshalStorage(channels)
	if err != nil {
		return nil, 1, errors.FaultErrorWrap(err, "Error marshalling voucher")
	}

	return channelsBytes, 0, nil
}

func updateChannel(ctx exec.VMContext, target address.Address, channel *PaymentChannel, amt *types.AttoFIL, validAt *types.BlockHeight) error {
	if target != channel.Target {
		return Errors[ErrWrongTarget]
	}

	if ctx.BlockHeight().LessThan(validAt) {
		return Errors[ErrTooEarly]
	}

	if ctx.BlockHeight().GreaterEqual(channel.Eol) {
		return Errors[ErrExpired]
	}

	if amt.GreaterThan(channel.Amount) {
		return Errors[ErrInsufficientChannelFunds]
	}

	if amt.LessEqual(channel.AmountRedeemed) {
		return Errors[ErrAlreadyWithdrawn]
	}

	// transfer funds to sender
	updateAmount := amt.Sub(channel.AmountRedeemed)
	_, _, err := ctx.Send(ctx.Message().From, "", updateAmount, nil)
	if err != nil {
		return err
	}

	// update amount redeemed from this channel
	channel.AmountRedeemed = amt

	return nil
}

func reclaim(ctx context.Context, vmctx exec.VMContext, byChannelID exec.Lookup, payer address.Address, chid *types.ChannelID, channel *PaymentChannel) error {
	amt := channel.Amount.Sub(channel.AmountRedeemed)
	if amt.LessEqual(types.ZeroAttoFIL) {
		return nil
	}

	// clean up
	err := byChannelID.Delete(ctx, chid.KeyString())
	if err != nil {
		return err
	}

	// send funds
	_, _, err = vmctx.Send(payer, "", amt, nil)
	if err != nil {
		return errors.RevertErrorWrap(err, "could not send update funds")
	}

	return nil
}

// Separator is the separator used when concatenating channel and amount in a
// voucher signature.
const separator = 0x0

// SignVoucher creates the signature for the given combination of
// channel, amount, validAt (earliest block height for redeem) and from address.
// It does so by signing the following bytes: (channelID | 0x0 | amount | 0x0 | validAt)
func SignVoucher(channelID *types.ChannelID, amount *types.AttoFIL, validAt *types.BlockHeight, addr address.Address, condition *types.Predicate, signer types.Signer) (types.Signature, error) {
	data, err := createVoucherSignatureData(channelID, amount, validAt, condition)
	if err != nil {
		return nil, err
	}
	return signer.SignBytes(data, addr)
}

// VerifyVoucherSignature returns whether the voucher's signature is valid
func VerifyVoucherSignature(payer address.Address, chid *types.ChannelID, amt *types.AttoFIL, validAt *types.BlockHeight, condition *types.Predicate, sig []byte) bool {
	data, err := createVoucherSignatureData(chid, amt, validAt, condition)
	// the only error is failure to encode the values
	if err != nil {
		return false
	}
	return types.IsValidSignature(data, payer, sig)
}

func createVoucherSignatureData(channelID *types.ChannelID, amount *types.AttoFIL, validAt *types.BlockHeight, condition *types.Predicate) ([]byte, error) {
	data := append(channelID.Bytes(), separator)
	data = append(data, amount.Bytes()...)
	data = append(data, separator)
	if condition != nil {
		data = append(data, condition.To.Bytes()...)
		data = append(data, []byte(condition.Method)...)
		encodedParams, err := abi.ToEncodedValues(condition.Params...)
		if err != nil {
			return []byte{}, err
		}
		data = append(data, encodedParams...)
	}
	return append(data, validAt.Bytes()...), nil
}

func withPayerChannels(ctx context.Context, storage exec.Storage, payer address.Address, f func(exec.Lookup) error) error {
	stateCid, err := actor.WithLookup(ctx, storage, storage.Head(), func(byPayer exec.Lookup) error {
		byChannelLookup, err := findByChannelLookup(ctx, storage, byPayer, payer)
		if err != nil {
			return err
		}

		// run inner function
		err = f(byChannelLookup)
		if err != nil {
			return err
		}

		// commit channel lookup
		commitedCID, err := byChannelLookup.Commit(ctx)
		if err != nil {
			return err
		}

		// if all payers channels are gone, delete the payer
		if byChannelLookup.IsEmpty() {
			return byPayer.Delete(ctx, payer.String())
		}

		// set payers channels into primary lookup
		return byPayer.Set(ctx, payer.String(), commitedCID)
	})
	if err != nil {
		return err
	}

	return storage.Commit(stateCid, storage.Head())
}

func withPayerChannelsForReading(ctx context.Context, storage exec.Storage, payer address.Address, f func(exec.Lookup) error) error {
	return actor.WithLookupForReading(ctx, storage, storage.Head(), func(byPayer exec.Lookup) error {
		byChannelLookup, err := findByChannelLookup(ctx, storage, byPayer, payer)
		if err != nil {
			return err
		}

		// run inner function
		return f(byChannelLookup)
	})
}

func findByChannelLookup(ctx context.Context, storage exec.Storage, byPayer exec.Lookup, payer address.Address) (exec.Lookup, error) {
	byChannelID, err := byPayer.Find(ctx, payer.String())
	if err != nil {
		if err == hamt.ErrNotFound {
			return actor.LoadLookup(ctx, storage, cid.Undef)
		}
		return nil, err
	}
	byChannelCID, ok := byChannelID.(cid.Cid)
	if !ok {
		return nil, errors.NewFaultError("Paymentbroker payer is not a Cid")
	}

	return actor.LoadTypedLookup(ctx, storage, byChannelCID, &PaymentChannel{})
}

func checkCondition(vmctx exec.VMContext, condition *types.Predicate, redeemerSuppliedParams []interface{}) (uint8, error) {
	params := append(condition.Params[:0:0], condition.Params...)
	params = append(params, redeemerSuppliedParams...)
	_, _, err := vmctx.Send(condition.To, condition.Method, types.NewZeroAttoFIL(), params)
	if err != nil {
		if errors.IsFault(err) {
			return errors.CodeError(err), err
		}
		return ErrConditionInvalid, errors.RevertErrorWrap(err, "failed to validate voucher condition")
	}
	return 0, nil
}
