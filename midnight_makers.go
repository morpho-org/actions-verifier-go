package calldataverifier

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	maxPayloadBytes            = 1_000_000
	maxAttributionSuffixBytes  = 256
	maxCompressedItemsBytes    = maxPayloadBytes - 5 - maxAttributionSuffixBytes
	maxDecompressedItemsBytes  = 6_000_000
	maxOfferTick               = 6744
	minRateRatifierTick        = 3372
	maxOfferTreeHeight         = 20
	maxOfferTimestamp          = 1_000_000_000_000 - 1
	secondsPerDay              = 86_400
	maturityTimeOfDay          = 15 * 3_600
	offerTypeID                = "0x9905214264a9fb7b6cc1b0e33db7a04687c6e4185a84755d29914314aa9d8906"
	priceRatifierV1OfferTypeID = "0x62a94097fbcb9b56e3cf7b4f7bb075b49540b1364815434ff4b16bc4decb303d"
	rateRatifierV1OfferTypeID  = "0x8e0f9c17bf7e919b5f22e8c6c4e787905f45864a4be441067bbd2e65b35c3de1"
	marketTypeID               = "0x510b3862f3816a109c9340b76972e8a30984246be06e034ae12ed2934220391a"
	collateralParamsTypeID     = "0x39ed3f928d24fd00574b1a02aba9c2483abcf5d9a3a366118c9a5aa29885b841"
)

var (
	errInvalidOfferPayload = errors.New("invalid Midnight offer payload")
	errOfferRootMismatch   = errors.New("ratifier data does not match the rebuilt offer tree")
)

var makerSides = map[string]struct {
	buy, reduceOnly bool
	unit            string
}{
	"midnight.make_lend":     {true, false, "assets"},
	"midnight.make_borrow":   {false, false, "assets"},
	"midnight.make_repay":    {true, true, "units"},
	"midnight.make_withdraw": {false, true, "units"},
}

type midnightOffer struct {
	Market                  midnightMarket
	Buy                     bool
	Maker                   common.Address
	Start, Expiry, Tick     *big.Int
	Group                   [32]byte
	Callback                common.Address
	CallbackData            []byte
	ReceiverIfMakerIsSeller common.Address
	Ratifier                common.Address
	ReduceOnly              bool
	MaxUnits, MaxAssets     *big.Int
	ContinuousFeeCap        *big.Int
}

type midnightPayloadItem struct {
	Offer        midnightOffer
	RatifierData []byte
}

type makerLeaf struct {
	offer        midnightOffer
	rate         *big.Int
	allowedTaker common.Address
}

type makerContext struct {
	midnightContext
	make MidnightOfferManagementIntent
	cap  *big.Int
	blue *makerBlueFunding
}

type makerBlueFunding struct {
	loanToken    common.Address
	assets       *big.Int
	salt         [32]byte
	callback     common.Address
	callbackData []byte
}

func mustType(t string, components []abi.ArgumentMarshaling) abi.Type {
	parsed, err := abi.NewType(t, "", components)
	if err != nil {
		panic(err)
	}
	return parsed
}

var (
	payloadItemsArguments = func() abi.Arguments {
		market := defaultSnapshot.abis["midnight"].Methods["withdraw"].Inputs[0]
		var marketComponents []abi.ArgumentMarshaling
		for i, elem := range market.Type.TupleElems {
			marketComponents = append(marketComponents, marshaling(market.Type.TupleRawNames[i], *elem))
		}
		field := func(name, t string) abi.ArgumentMarshaling { return abi.ArgumentMarshaling{Name: name, Type: t} }
		offer := []abi.ArgumentMarshaling{
			{Name: "market", Type: "tuple", Components: marketComponents},
			field("buy", "bool"), field("maker", "address"), field("start", "uint256"),
			field("expiry", "uint256"), field("tick", "uint256"), field("group", "bytes32"),
			field("callback", "address"), field("callbackData", "bytes"),
			field("receiverIfMakerIsSeller", "address"), field("ratifier", "address"),
			field("reduceOnly", "bool"), field("maxUnits", "uint256"), field("maxAssets", "uint256"),
			field("continuousFeeCap", "uint256"),
		}
		return abi.Arguments{{Type: mustType("tuple[]", []abi.ArgumentMarshaling{
			{Name: "offer", Type: "tuple", Components: offer},
			field("ratifierData", "bytes"),
		})}}
	}()
	priceRatifierDataArguments = abi.Arguments{
		{Type: mustType("bytes32", nil)}, {Type: mustType("uint256", nil)},
		{Type: mustType("bytes32[]", nil)}, {Type: mustType("address", nil)},
	}
	rateRatifierDataArguments = abi.Arguments{
		{Type: mustType("bytes32", nil)}, {Type: mustType("uint256", nil)},
		{Type: mustType("bytes32[]", nil)}, {Type: mustType("uint256", nil)}, {Type: mustType("address", nil)},
	}
)

func marshaling(name string, t abi.Type) abi.ArgumentMarshaling {
	result := abi.ArgumentMarshaling{Name: name, Type: t.String()}
	elem := &t
	suffix := ""
	for elem.T == abi.SliceTy {
		elem, suffix = elem.Elem, suffix+"[]"
	}
	if elem.T == abi.TupleTy {
		result.Type = "tuple" + suffix
		for i, child := range elem.TupleElems {
			result.Components = append(result.Components, marshaling(elem.TupleRawNames[i], *child))
		}
	}
	return result
}

func verifyMidnightMake(intent MidnightOfferManagementIntent, calls []CalldataCall, chain Deployment) ([]DecodedCalldataCall, error) {
	call, err := selectMain(calls, 1<<53-1)
	if err != nil {
		return nil, err
	}
	if err := target(call, chain.MidnightBundlesV2); err != nil {
		return nil, err
	}
	decoded, err := decodeCall(call, defaultSnapshot.abis["midnightBundle"], false)
	if err != nil {
		return nil, err
	}
	if decoded.FunctionName != "midnightBundlesV2CancelAndMake" {
		return nil, callError("function", &call)
	}
	side := makerSides[intent.Action]
	if intent.Amount.Unit != side.unit {
		return nil, amountError("amount.unit", call)
	}
	cap, err := readUint(intent.Amount.Value, "amount", call)
	if err != nil {
		return nil, err
	}
	c := makerContext{midnightContext: midnightContext{
		intent: MidnightMarketIntent{CommonIntent: intent.CommonIntent, Funding: intent.Funding},
		chain:  chain, call: call,
	}, make: intent, cap: cap}
	if c.blue, err = c.blueFunding(decoded.Args); err != nil {
		return nil, err
	}
	if err := c.envelope(decoded.Args); err != nil {
		return nil, err
	}
	offers, err := c.offers(decoded.Args)
	if err != nil {
		return nil, err
	}
	pulls, err := c.collateralSupplies(decoded.Args, offers)
	if err != nil {
		return nil, err
	}
	if side.buy {
		token, spender, assets := offers[0].Market.LoanToken, chain.Midnight, new(big.Int)
		if c.blue != nil {
			if c.blue.loanToken != token {
				return nil, &CalldataTargetError{detail("funding_source.market_id", &call, nil)}
			}
			spender = chain.MidnightBundlesV2
		}
		if intent.Action == "midnight.make_lend" {
			assets = cap
		}
		pulls = append(pulls, midnightPull{token: token, assets: assets, spender: spender})
	}
	checked, err := c.prerequisites(calls[:len(calls)-1], pulls, true)
	if err != nil {
		return nil, err
	}
	return append(checked, decoded), nil
}

func (c makerContext) blueFunding(args []any) (*makerBlueFunding, error) {
	market, assets, salt := converted[blueMarketParams](args[0]), args[1].(*big.Int), args[2].([32]byte)
	var source *MidnightFundingSource
	if c.make.Action == "midnight.make_lend" {
		source = c.make.FundingSource
	}
	if source != nil && source.Type != "wallet" && source.Type != "variable_rate_market" {
		return nil, &CalldataUnsupportedError{detail("funding_source", &c.call, nil)}
	}
	if source == nil || source.Type != "variable_rate_market" {
		if market.LoanToken != (common.Address{}) || market.CollateralToken != (common.Address{}) ||
			market.Oracle != (common.Address{}) || market.Irm != (common.Address{}) || market.Lltv.Sign() != 0 ||
			assets.Sign() != 0 || salt != [32]byte{} {
			return nil, callError("funding_source", &c.call)
		}
		return nil, nil
	}
	callbackData, err := defaultSnapshot.market.Pack(market)
	if err != nil || !strings.EqualFold(crypto.Keccak256Hash(callbackData).Hex(), source.MarketID) {
		return nil, &CalldataTargetError{detail("funding_source.market_id", &c.call, err)}
	}
	if c.cap.Sign() == 0 || c.cap.BitLen() > 128 || assets.Cmp(c.cap) != 0 {
		return nil, amountError("amount", c.call)
	}
	callback, ok := blueBuyCallbackAddress(c.chain, common.HexToAddress(c.make.Account), salt)
	if !ok {
		return nil, &CalldataUnsupportedError{detail("funding_source", &c.call, nil)}
	}
	return &makerBlueFunding{
		loanToken: market.LoanToken, assets: assets, salt: salt, callback: callback, callbackData: callbackData,
	}, nil
}

func (c makerContext) envelope(args []any) error {
	var zeroHash [32]byte
	if args[7].(*big.Int).Sign() != 0 || args[8].(*big.Int).Sign() != 0 || args[9].(*big.Int).Sign() != 0 ||
		args[10].(uint8) != 0 || args[11].([32]byte) != zeroHash || args[12].([32]byte) != zeroHash {
		return callError("root_signature", &c.call)
	}
	if reflect.ValueOf(args[13]).Len() != 0 {
		return callError("cancellations", &c.call)
	}
	if args[15].(*big.Int).Sign() == 0 {
		return amountError("deadline", c.call)
	}
	return nil
}

func (c makerContext) offers(args []any) ([]midnightOffer, error) {
	call, offers := c.call, c.make.Offers
	var ratifier string
	if offers != nil && offers.Type == "price" {
		ratifier = c.chain.PriceRatifierV1
	} else if offers != nil && offers.Type == "rate" {
		ratifier = c.chain.RateRatifierV1
	}
	if !sameAddress(args[5].(common.Address).Hex(), ratifier) {
		return nil, &CalldataTargetError{detail("ratifier", &call, nil)}
	}
	if len(offers.Entries) == 0 || slices.ContainsFunc(offers.Entries, func(entry MidnightMakerOfferEntry) bool { return entry.missing }) {
		return nil, callError("offers.entries", &call)
	}
	items, err := decodeOfferPayload(args[14].([]byte), len(offers.Entries))
	if err != nil {
		return nil, &CalldataDecodeError{detail("payload", &call, err)}
	}
	if len(items) != len(offers.Entries) {
		return nil, callError("offers.entries", &call)
	}
	if c.blue != nil {
		members := make([][]byte, len(items))
		for i, item := range items {
			offer := item.Offer
			offer.MaxAssets, offer.Callback, offer.CallbackData = c.cap, common.Address{}, nil
			member := protocolOfferHash(offer, [32]byte{})
			members[i] = member[:]
		}
		slices.SortFunc(members, bytes.Compare)
		if crypto.Keccak256Hash(members...) != c.blue.salt {
			return nil, callError("callback_salt", &call)
		}
	}
	leaves, root, group, err := rebuildOfferTree(items, offers.Type == "rate")
	if err != nil && !errors.Is(err, errOfferRootMismatch) {
		return nil, &CalldataCallError{detail("payload", &call, err)}
	}
	if err != nil || root != args[6].([32]byte) {
		return nil, callError("root", &call)
	}
	result := make([]midnightOffer, len(leaves))
	for i, leaf := range leaves {
		if leaf.allowedTaker != (common.Address{}) {
			return nil, callError("offers.allowed_taker", &call)
		}
		if err := c.offer(leaf, offers.Entries[i], ratifier); err != nil {
			return nil, err
		}
		result[i] = leaf.offer
	}
	for _, leaf := range leaves {
		if leaf.offer.Group != group {
			return nil, callError("offers.group", &call)
		}
	}
	return result, nil
}

func (c makerContext) offer(leaf makerLeaf, entry MidnightMakerOfferEntry, ratifier string) error {
	call, offer, side := c.call, leaf.offer, makerSides[c.make.Action]
	id, err := c.marketID(offer.Market)
	if err != nil {
		return err
	}
	if !offer.Market.ChainId.IsUint64() || offer.Market.ChainId.Uint64() != c.make.ChainID ||
		!sameAddress(offer.Market.Midnight.Hex(), c.chain.Midnight) || !strings.EqualFold(id.Hex(), entry.MarketID) {
		return &CalldataTargetError{detail("offers.market_id", &call, nil)}
	}
	if err := recipient(offer.Maker, c.make.Account, "offers.maker", call); err != nil {
		return err
	}
	receiver := c.make.Account
	if side.buy {
		receiver = common.Address{}.Hex()
	}
	if err := recipient(offer.ReceiverIfMakerIsSeller, receiver, "offers.receiver", call); err != nil {
		return err
	}
	callback, callbackData, cap := common.Address{}, []byte(nil), c.cap
	if c.blue != nil {
		callback, callbackData, cap = c.blue.callback, c.blue.callbackData, maxUint128
	}
	if offer.Buy != side.buy || offer.ReduceOnly != side.reduceOnly || !sameAddress(offer.Ratifier.Hex(), ratifier) ||
		offer.Callback != callback || !bytes.Equal(offer.CallbackData, callbackData) || offer.ContinuousFeeCap.Sign() != 0 {
		return callError("offers", &call)
	}
	if offer.Start.Cmp(new(big.Int).SetUint64(entry.StartsAt)) != 0 || offer.Expiry.Cmp(new(big.Int).SetUint64(entry.ExpiresAt)) != 0 {
		return amountError("offers.expires_at", call)
	}
	if c.make.Offers.Type == "price" || entry.Tick != "" {
		if err := expectMidnightAmount(offer.Tick, entry.Tick, "offers.tick", call); err != nil {
			return err
		}
	}
	if c.make.Offers.Type == "rate" {
		if err := expectMidnightAmount(leaf.rate, entry.RateWAD, "offers.rate_wad", call); err != nil {
			return err
		}
	}
	capped, other := offer.MaxAssets, offer.MaxUnits
	if side.unit == "units" {
		capped, other = other, capped
	}
	if capped.Cmp(cap) != 0 || other.Sign() != 0 {
		return amountError("amount", call)
	}
	return nil
}

func (c makerContext) marketID(market midnightMarket) (common.Hash, error) {
	for i := 1; i < len(market.CollateralParams); i++ {
		if bytes.Compare(market.CollateralParams[i-1].Token[:], market.CollateralParams[i].Token[:]) >= 0 {
			return common.Hash{}, &CalldataTargetError{detail("market", &c.call, nil)}
		}
	}
	id, err := midnightMarketID(market)
	if err != nil {
		return common.Hash{}, &CalldataTargetError{detail("market", &c.call, err)}
	}
	return id, nil
}

func (c makerContext) collateralSupplies(args []any, offers []midnightOffer) ([]midnightPull, error) {
	call := c.call
	market := converted[midnightMarket](args[3])
	supplies := midnightTransfers(args[4])
	wrapped := args[16].(common.Address)
	value, err := readUint(call.Value, "value", call)
	if err != nil {
		return nil, err
	}
	var requested []MidnightCollateralTransfer
	if c.make.Action == "midnight.make_borrow" {
		requested = c.make.CollateralSupplies
	}
	if len(requested) == 0 {
		if market.ChainId.Sign() != 0 || market.Midnight != (common.Address{}) || market.LoanToken != (common.Address{}) ||
			len(market.CollateralParams) != 0 || market.Maturity.Sign() != 0 || market.RcfThreshold.Sign() != 0 ||
			market.EnterGate != (common.Address{}) || market.LiquidatorGate != (common.Address{}) ||
			len(supplies) != 0 || (c.blue == nil && (value.Sign() != 0 || wrapped != (common.Address{}))) {
			return nil, callError("collateral_supplies", &call)
		}
		if c.blue == nil || (value.Sign() == 0 && wrapped == (common.Address{})) {
			return nil, nil
		}
		if value.Sign() == 0 || !sameAddress(c.blue.loanToken.Hex(), c.chain.WNative) ||
			!sameAddress(wrapped.Hex(), c.chain.WNative) || value.Cmp(c.blue.assets) > 0 {
			return nil, amountError("value", call)
		}
		return nil, checkNativeBudget(value, c.make.Funding, call)
	}
	id, err := c.marketID(market)
	if err != nil {
		return nil, err
	}
	if !market.ChainId.IsUint64() || market.ChainId.Uint64() != c.make.ChainID || !sameAddress(market.Midnight.Hex(), c.chain.Midnight) {
		return nil, &CalldataTargetError{detail("market", &call, nil)}
	}
	for _, offer := range offers {
		offerID, err := c.marketID(offer.Market)
		if err != nil {
			return nil, err
		}
		if offerID != id {
			return nil, &CalldataTargetError{detail("market", &call, nil)}
		}
	}
	pulls, err := c.collateral(supplies, requested, market, "", false)
	if err != nil {
		return nil, err
	}
	if value.Sign() == 0 && wrapped == (common.Address{}) {
		return pulls, nil
	}
	nativeCap := new(big.Int)
	for _, pull := range pulls {
		if sameAddress(pull.token.Hex(), c.chain.WNative) {
			nativeCap = pull.assets
			break
		}
	}
	if value.Sign() == 0 || !sameAddress(wrapped.Hex(), c.chain.WNative) || value.Cmp(nativeCap) > 0 {
		return nil, amountError("value", call)
	}
	return pulls, checkNativeBudget(value, c.make.Funding, call)
}

func midnightMarketID(market midnightMarket) (common.Hash, error) {
	method := defaultSnapshot.abis["midnight"].Methods["withdraw"]
	encoded, err := abi.Arguments{method.Inputs[0]}.Pack(market)
	if err != nil {
		return common.Hash{}, err
	}
	creationHash := crypto.Keccak256(common.FromHex("0x600b380380600b5f395ff3"), encoded)
	return crypto.Keccak256Hash([]byte{255}, market.Midnight[:], make([]byte, 32), creationHash), nil
}

func decodeOfferPayload(payload []byte, maxItems int) ([]midnightPayloadItem, error) {
	if len(payload) < 5 || len(payload) > maxPayloadBytes || payload[0] != 1 ||
		uint64(len(payload)) != 5+uint64(binary.BigEndian.Uint32(payload[1:5])) {
		return nil, errInvalidOfferPayload
	}
	compressed := payload[5:]
	if len(compressed) == 0 || len(compressed) > maxCompressedItemsBytes {
		return nil, errInvalidOfferPayload
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxDecompressedItemsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDecompressedItemsBytes || len(data) < 64 {
		return nil, errInvalidOfferPayload
	}
	if count := new(big.Int).SetBytes(data[32:64]); count.Cmp(big.NewInt(int64(maxItems))) > 0 {
		return nil, errInvalidOfferPayload
	}
	args, err := unpackCanonical(payloadItemsArguments, data, true)
	if err != nil {
		return nil, err
	}
	items := converted[[]midnightPayloadItem](args[0])
	if len(items) == 0 {
		return nil, errInvalidOfferPayload
	}
	for _, item := range items {
		if !validPayloadOffer(item.Offer) {
			return nil, errInvalidOfferPayload
		}
	}
	return items, nil
}

func emptyOffer(offer midnightOffer) bool {
	market := offer.Market
	var zero common.Address
	return market.ChainId.Sign() == 0 && market.Midnight == zero && market.LoanToken == zero &&
		len(market.CollateralParams) == 0 && market.Maturity.Sign() == 0 && market.RcfThreshold.Sign() == 0 &&
		market.EnterGate == zero && market.LiquidatorGate == zero && !offer.Buy && offer.Maker == zero &&
		offer.Start.Sign() == 0 && offer.Expiry.Sign() == 0 && offer.Tick.Sign() == 0 && offer.Group == [32]byte{} &&
		offer.Callback == zero && len(offer.CallbackData) == 0 && offer.ReceiverIfMakerIsSeller == zero &&
		offer.Ratifier == zero && !offer.ReduceOnly && offer.MaxUnits.Sign() == 0 && offer.MaxAssets.Sign() == 0 &&
		offer.ContinuousFeeCap.Sign() == 0
}

func validPayloadOffer(offer midnightOffer) bool {
	if emptyOffer(offer) {
		return true
	}
	collaterals := offer.Market.CollateralParams
	if len(collaterals) == 0 || len(collaterals) > 128 {
		return false
	}
	for i, collateral := range collaterals {
		if !validMidnightCollateral(collateral) || collateral.Token == (common.Address{}) ||
			(i > 0 && bytes.Compare(collaterals[i-1].Token[:], collateral.Token[:]) >= 0) {
			return false
		}
	}
	maxTimestamp := big.NewInt(maxOfferTimestamp)
	maturity := offer.Market.Maturity
	if maturity.Cmp(maxTimestamp) > 0 || maturity.Uint64()%secondsPerDay != maturityTimeOfDay ||
		offer.Start.Cmp(maxTimestamp) > 0 || offer.Expiry.Cmp(maxTimestamp) > 0 || offer.Expiry.Cmp(offer.Start) < 0 {
		return false
	}
	if offer.Buy && offer.ReceiverIfMakerIsSeller != (common.Address{}) {
		return false
	}
	return offer.Expiry.Cmp(maturity) <= 0 && offer.Tick.Cmp(big.NewInt(maxOfferTick)) <= 0 &&
		(offer.MaxUnits.Sign() == 0) != (offer.MaxAssets.Sign() == 0) &&
		offer.MaxUnits.BitLen() <= 128 && offer.MaxAssets.BitLen() <= 128
}

func rebuildOfferTree(items []midnightPayloadItem, rate bool) ([]makerLeaf, [32]byte, [32]byte, error) {
	var root, group [32]byte
	arguments := priceRatifierDataArguments
	if rate {
		arguments = rateRatifierDataArguments
	}
	leaves := make([]makerLeaf, len(items))
	for i, item := range items {
		decoded, err := arguments.Unpack(item.RatifierData)
		if err != nil {
			return nil, root, group, err
		}
		leaves[i] = makerLeaf{offer: item.Offer, allowedTaker: decoded[len(decoded)-1].(common.Address)}
		if rate {
			leaves[i].rate = decoded[3].(*big.Int)
			if item.Offer.Tick.Cmp(big.NewInt(minRateRatifierTick)) < 0 {
				return nil, root, group, errInvalidOfferPayload
			}
		}
	}
	first := leaves[0].offer
	for _, leaf := range leaves {
		offer := leaf.offer
		if offer.Maker != first.Maker || offer.Buy != first.Buy || offer.Ratifier != first.Ratifier ||
			offer.Market.LoanToken != first.Market.LoanToken || offer.Market.ChainId.Cmp(first.Market.ChainId) != 0 ||
			offer.Market.Midnight != first.Market.Midnight || offer.MaxUnits.Cmp(first.MaxUnits) != 0 ||
			offer.MaxAssets.Cmp(first.MaxAssets) != 0 || (!offer.Buy && offer.ReceiverIfMakerIsSeller == (common.Address{})) {
			return nil, root, group, errInvalidOfferPayload
		}
	}
	hashes := make([][32]byte, len(leaves))
	members := make([][]byte, len(leaves))
	seen := make(map[[32]byte]bool, len(leaves))
	for i, leaf := range leaves {
		hashes[i] = hashMakerLeaf(leaf, rate, false)
		if seen[hashes[i]] {
			return nil, root, group, errInvalidOfferPayload
		}
		seen[hashes[i]] = true
		member := hashMakerLeaf(leaf, rate, true)
		members[i] = member[:]
	}
	slices.SortFunc(members, bytes.Compare)
	group = crypto.Keccak256Hash(members...)
	padding := hashMakerLeaf(makerLeaf{offer: midnightOffer{
		Market: midnightMarket{ChainId: new(big.Int), Maturity: new(big.Int), RcfThreshold: new(big.Int)},
		Start:  new(big.Int), Expiry: new(big.Int), Tick: new(big.Int),
		MaxUnits: new(big.Int), MaxAssets: new(big.Int), ContinuousFeeCap: new(big.Int),
	}, rate: new(big.Int)}, rate, false)
	width := 1
	for width < len(hashes) {
		width <<= 1
	}
	if width > 1<<maxOfferTreeHeight {
		return nil, root, group, errInvalidOfferPayload
	}
	layers := [][][32]byte{append(hashes, slices.Repeat([][32]byte{padding}, width-len(hashes))...)}
	for level := layers[0]; len(level) > 1; level = layers[len(layers)-1] {
		next := make([][32]byte, len(level)/2)
		for i := range next {
			next[i] = crypto.Keccak256Hash(level[2*i][:], level[2*i+1][:])
		}
		layers = append(layers, next)
	}
	root = layers[len(layers)-1][0]
	for i, leaf := range leaves {
		proof := make([][32]byte, len(layers)-1)
		for layer := range proof {
			proof[layer] = layers[layer][(i>>layer)^1]
		}
		values := []any{root, big.NewInt(int64(i)), proof}
		if rate {
			values = append(values, leaf.rate)
		}
		expected, err := arguments.Pack(append(values, leaf.allowedTaker)...)
		if err != nil || !bytes.Equal(expected, items[i].RatifierData) {
			return nil, root, group, errOfferRootMismatch
		}
	}
	return leaves, root, group, nil
}

func hashMakerLeaf(leaf makerLeaf, rate, member bool) [32]byte {
	offer := leaf.offer
	word := func(value any) []byte {
		switch v := value.(type) {
		case *big.Int:
			return common.BigToHash(v).Bytes()
		case common.Address:
			return common.BytesToHash(v[:]).Bytes()
		case bool:
			if v {
				return common.BigToHash(big.NewInt(1)).Bytes()
			}
			return make([]byte, 32)
		case [32]byte:
			return v[:]
		}
		panic("unsupported leaf word")
	}
	typeID, price := priceRatifierV1OfferTypeID, any(offer.Tick)
	if rate {
		typeID, price = rateRatifierV1OfferTypeID, leaf.rate
	}
	group := offer.Group
	if member {
		group = [32]byte{}
	}
	var encoded []byte
	for _, value := range []any{
		common.HexToHash(typeID), midnightMarketHash(offer.Market), offer.Buy, offer.Maker, offer.Start,
		offer.Expiry, price, leaf.allowedTaker, group, offer.Callback, crypto.Keccak256Hash(offer.CallbackData),
		offer.ReceiverIfMakerIsSeller, offer.Ratifier, offer.ReduceOnly, offer.MaxUnits, offer.MaxAssets,
		offer.ContinuousFeeCap,
	} {
		if hash, ok := value.(common.Hash); ok {
			value = [32]byte(hash)
		}
		encoded = append(encoded, word(value)...)
	}
	return crypto.Keccak256Hash(encoded)
}

func protocolOfferHash(offer midnightOffer, group [32]byte) [32]byte {
	market := midnightMarketHash(offer.Market)
	var encoded []byte
	for _, word := range [][]byte{
		common.FromHex(offerTypeID), market[:], boolWord(offer.Buy),
		common.BytesToHash(offer.Maker[:]).Bytes(), common.BigToHash(offer.Start).Bytes(),
		common.BigToHash(offer.Expiry).Bytes(), common.BigToHash(offer.Tick).Bytes(), group[:],
		common.BytesToHash(offer.Callback[:]).Bytes(), crypto.Keccak256(offer.CallbackData),
		common.BytesToHash(offer.ReceiverIfMakerIsSeller[:]).Bytes(), common.BytesToHash(offer.Ratifier[:]).Bytes(),
		boolWord(offer.ReduceOnly), common.BigToHash(offer.MaxUnits).Bytes(), common.BigToHash(offer.MaxAssets).Bytes(),
		common.BigToHash(offer.ContinuousFeeCap).Bytes(),
	} {
		encoded = append(encoded, word...)
	}
	return crypto.Keccak256Hash(encoded)
}

func boolWord(v bool) []byte {
	word := make([]byte, 32)
	if v {
		word[31] = 1
	}
	return word
}

func midnightMarketHash(market midnightMarket) [32]byte {
	collaterals := make([][]byte, len(market.CollateralParams))
	for i, collateral := range market.CollateralParams {
		collaterals[i] = crypto.Keccak256(
			common.FromHex(collateralParamsTypeID), common.BytesToHash(collateral.Token[:]).Bytes(),
			common.BigToHash(collateral.Lltv).Bytes(), common.BigToHash(collateral.LiquidationCursor).Bytes(),
			common.BytesToHash(collateral.Oracle[:]).Bytes(),
		)
	}
	return crypto.Keccak256Hash(
		common.FromHex(marketTypeID), common.BigToHash(market.ChainId).Bytes(),
		common.BytesToHash(market.Midnight[:]).Bytes(), common.BytesToHash(market.LoanToken[:]).Bytes(),
		crypto.Keccak256(collaterals...), common.BigToHash(market.Maturity).Bytes(),
		common.BigToHash(market.RcfThreshold).Bytes(), common.BytesToHash(market.EnterGate[:]).Bytes(),
		common.BytesToHash(market.LiquidatorGate[:]).Bytes(),
	)
}
