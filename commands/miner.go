package commands

import (
	"fmt"
	"io"

	cmds "gx/ipfs/QmRv6ddf7gkiEgBs1LADv3vC1mkVGPZEfByoiiVybjE9Mc/go-ipfs-cmds"
	"gx/ipfs/QmVmDhyTTUcQXFD1rRQ64fGLMSAoaQvNH3hwuaCFAPq2hy/errors"
	"gx/ipfs/QmcZfnkapfECQGcLZaf9B79NRg7cRa9EnZh4LSbkCzwNvY/go-cid"
	cmdkit "gx/ipfs/QmceUdzxkimdYsgtX733uNgzf1DLHyBKN6ehGSp85ayppM/go-ipfs-cmdkit"

	"github.com/filecoin-project/go-filecoin/abi"
	"github.com/filecoin-project/go-filecoin/core"
	"github.com/filecoin-project/go-filecoin/types"
)

var minerCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Miner operations",
	},
	Subcommands: map[string]*cmds.Command{
		"create":  minerCreateCmd,
		"add-ask": minerAddAskCmd,
	},
}

var minerCreateCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Create a new file miner",
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("plege", true, false, "the size of the pledge for the miner"),
	},
	Options: []cmdkit.Option{
		cmdkit.StringOption("from", "address to send from"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		n := GetNode(env)

		fromAddr, err := addressWithDefault(req.Options["from"], n)
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid from address"), cmdkit.ErrNormal)
			return
		}

		pledge, err := toBigInt(req.Arguments[0])
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid pledge"), cmdkit.ErrNormal)
			return
		}

		params, err := abi.ToEncodedValues(pledge)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		msg := types.NewMessage(fromAddr, core.StorageMarketAddress, nil, "createMiner", params)
		if err := n.AddNewMessage(req.Context, msg); err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		msgCid, err := msg.Cid()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		waitForMessage(n, msgCid, func(blk *types.Block, msg *types.Message, receipt *types.MessageReceipt) {
			address, err := abi.Deserialize(receipt.Return, abi.Address)
			if err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}
			re.Emit(address.Val) // nolint: errcheck
		})
	},
	Type: types.Address(""),
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeTypedEncoder(func(req *cmds.Request, w io.Writer, addr *types.Address) error {
			_, err := fmt.Fprintln(w, *addr)
			return err
		}),
	},
}

var minerAddAskCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Add an ask",
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("miner", true, false, "the address of the miner owning the ask"),
		cmdkit.StringArg("size", true, false, "size in bytes of the ask"),
		cmdkit.StringArg("price", true, false, "the price of the ask"),
	},
	Options: []cmdkit.Option{
		cmdkit.StringOption("from", "address to send the ask from"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		n := GetNode(env)

		fromAddr, err := addressWithDefault(req.Options["from"], n)
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid from address"), cmdkit.ErrNormal)
			return
		}

		minerAddr, err := types.ParseAddress(req.Arguments[0])
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid miner address"), cmdkit.ErrNormal)
			return
		}

		size, err := toBigInt(req.Arguments[1])
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid sizes"), cmdkit.ErrNormal)
			return
		}

		price, err := toBigInt(req.Arguments[2])
		if err != nil {
			re.SetError(errors.Wrap(err, "invalid price"), cmdkit.ErrNormal)
			return
		}

		params, err := abi.ToEncodedValues(price, size)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		msg := types.NewMessage(fromAddr, minerAddr, nil, "addAsk", params)
		if err := n.AddNewMessage(req.Context, msg); err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		c, err := msg.Cid()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		re.Emit(c) // nolint: errcheck
	},
	Type: cid.Cid{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeTypedEncoder(func(req *cmds.Request, w io.Writer, c *cid.Cid) error {
			return PrintString(w, c)
		}),
	},
}
