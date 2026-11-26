# TypeScript test parity

Reference: `@morpho-org/calldata-verifier` at
`71a07c480538fe4925d27411bd95d95a1b93b4ae`.

All **208 declarations** are accounted for: **23 mapped** to existing Go
tests, **176 ported** as **787 distinct verifier inputs**, and **9 JS-only**.
A declaration using `it.each` or `test.each` may expand to multiple tests. The
parenthesized counts below are distinct verifier inputs per ported declaration,
including distinct verifier calls made within a single test.

The five `ts-parity-*.json` files were recorded by running the pinned suite with a
test-only wrapper around `verifyCalldata`. They preserve the inputs, decoded call
trees and ABI arguments, or the reported error class, field and call ID. The Go
replay normalizes ABI integers to decimal strings, hex to lowercase, and tuple
field names to lowercase so it compares values rather than language types. If an
input cannot enter the typed API, the replay checks that Go's JSON decoder
rejects it instead. Long repeated fixture strings are stored once per file and
referenced by `$p<N>` (a zero-based index into `strings`). Go tests require no
TypeScript checkout, JavaScript runtime, or network access.
See [regeneration instructions](parity-generation.md) when updating the pin.

| TypeScript source | Declaration | Layer | Go counterpart |
| --- | --- | --- | --- |
| `actionProof.test.ts:148` | rejects b64=%j without crit | 6 | Ported: `TestParityProof/actionProof.test.ts:148` (4) |
| `actionProof.test.ts:163` | rejects invalid proof JWK $field | 6 | Ported: `TestParityProof/actionProof.test.ts:163` (9) |
| `actionProof.test.ts:188` | rejects a string-coercible JWK %s | 6 | Ported: `TestParityProof/actionProof.test.ts:188` (2) |
| `actionProof.test.ts:199` | rejects invalid proof header $field | 6 | Ported: `TestParityProof/actionProof.test.ts:199` (8) |
| `actionProof.test.ts:222` | rejects an independently signed invalid $field claim | 6 | Ported: `TestParityProof/actionProof.test.ts:222` (5) |
| `actionProof.test.ts:250` | rejects $field | 6 | Ported: `TestParityProof/actionProof.test.ts:250` (7) |
| `actionProof.test.ts:279` | rejects a correctly signed response whose data is not an object | 6 | Ported: `TestParityProof/actionProof.test.ts:279` (1) |
| `actionProof.test.ts:291` | rejects the signed exchange with an invalid $field | 6 | Ported: `TestParityProof/actionProof.test.ts:291` (3) |
| `actionProof.test.ts:336` | rejects a signed null $claim | 6 | Ported: `TestParityProof/actionProof.test.ts:336` (2) |
| `actionProof.test.ts:347` | rejects a caller-supplied infinite verification time | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:358` | rejects a finite verification time outside the supported date range | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:369` | rejects a signed negative not-before date outside the numeric range | 6 | Ported: `TestParityProof/actionProof.test.ts:369` (1) |
| `actionProof.test.ts:382` | rejects a mismatch in the signed request method alone | 6 | Ported: `TestParityProof/actionProof.test.ts:382` (1) |
| `actionProof.test.ts:395` | requires %s body bytes, not an array | 6 | JS-only: The Go proof fields are byte slices; JS arrays are a distinct runtime type. |
| `actionProof.test.ts:410` | accepts a cross-realm Uint8Array as the %s body | 6 | JS-only: JavaScript realm identity does not apply to Go byte slices. |
| `actionProof.test.ts:428` | rejects a %s body with the wrong byte view (%p) | 6 | JS-only: JS byte-view branding and Symbol.toStringTag are not Go input types. |
| `actionProof.test.ts:452` | rejects caller-provided array-like calls even when all fields match | 6 | JS-only: Array-like objects cannot be passed as a Go slice of calls. The signed-response variant is replayed separately. |
| `actionProof.test.ts:461` | rejects signed array-like calls even when all fields match | 6 | Ported: `TestParityProof/actionProof.test.ts:461` (1) |
| `actionProof.test.ts:475` | wraps a malformed base64url quantum in a typed proof error | 6 | Ported: `TestParityProof/actionProof.test.ts:475` (1) |
| `actionProof.test.ts:484` | rejects extra unsigned calls even when every signed call matches | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:494` | $name | 6 | Mapped: `TestActionProofVectors` |
| `actionProof.test.ts:519` | rejects a proof with a different expected issuer | 6 | Ported: `TestParityProof/actionProof.test.ts:519` (1) |
| `actionProof.test.ts:528` | only prepared successful approvals allow calls to execute | 6 | Mapped: `TestActionProofVectors` |
| `actionProof.test.ts:545` | rejects a proof without a caller-supplied verification time | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:558` | returns the same decoded calls as the synchronous two-argument path | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:566` | still checks calls statically after authenticating them | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:577` | rejects a byte change that leaves the parsed response identical | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:592` | rejects a change to only response.$field | 6 | Ported: `TestParityProof/actionProof.test.ts:592` (2) |
| `actionProof.test.ts:609` | rejects a change to only request.contentType | 6 | Ported: `TestParityProof/actionProof.test.ts:609` (1) |
| `actionProof.test.ts:626` | hashes and parses a single response body snapshot | 6 | JS-only: The Go proof body is a field on a value struct, not a JavaScript getter. |
| `actionProof.test.ts:643` | parses the authenticated copy after the original response buffer changes | 6 | JS-only: The JS test uses a mocked SHA call to mutate the caller's buffer mid-verification. Go uses synchronous stdlib SHA; mutating the caller's bytes concurrently would be a data race. Go clones the body before hashing and parsing. |
| `actionProof.test.ts:668` | rejects a call with a different %s | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:684` | rejects reordered or missing calls | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:697` | rejects a different chain | 6 | Mapped: `TestActionProofBinding` |
| `actionProof.test.ts:711` | rejects a correctly signed prepared response without a chain ID | 6 | Ported: `TestParityProof/actionProof.test.ts:711` (1) |
| `blue.test.ts:32` | verifies %s and rejects a different market | 3 | Mapped: `TestBlueVectors` |
| `blue.test.ts:50` | checks the amount, fees, and allowed approval spender | 3 | Ported: `TestParityBlue/blue.test.ts:50` (7) |
| `blue.test.ts:99` | rejects a Blue fee above the requested rate but within max_fee_bps | 3 | Ported: `TestParityBlue/blue.test.ts:99` (2) |
| `blue.test.ts:110` | rejects a fee over the caller cap on $intent.action | 3 | Ported: `TestParityBlue/blue.test.ts:110` (4) |
| `blue.test.ts:125` | rejects a Blue withdrawal selector for a supply | 3 | Ported: `TestParityBlue/blue.test.ts:125` (1) |
| `blue.test.ts:143` | rejects a non-string Blue market with a typed error | 3 | Ported: `TestParityBlue/blue.test.ts:143` (1) |
| `blue.test.ts:156` | rejects a zero encoded Blue supply even if the requested amount is zero | 3 | Ported: `TestParityBlue/blue.test.ts:156` (1) |
| `blue.test.ts:183` | rejects unknown units on fixed-asset and position amounts | 3 | Ported: `TestParityBlue/blue.test.ts:183` (2) |
| `blue.test.ts:201` | requires authorization for native Blue supply | 3 | Ported: `TestParityBlue/blue.test.ts:201` (6) |
| `blue.test.ts:265` | rejects a missing amount on $intent.action | 3 | Ported: `TestParityBlue/blue.test.ts:265` (2) |
| `blue.test.ts:275` | rejects native Blue funding with an embedded ERC-2612 permit | 3 | Ported: `TestParityBlue/blue.test.ts:275` (2) |
| `blue.test.ts:316` | rejects unauthorized or duplicated delegation | 3 | Ported: `TestParityBlue/blue.test.ts:316` (2) |
| `blue.test.ts:337` | rejects Blue authorization with invalid %s | 3 | Ported: `TestParityBlue/blue.test.ts:337` (3) |
| `blue.test.ts:372` | requires a zero approval to be followed by a positive approval | 3 | Ported: `TestParityBlue/blue.test.ts:372` (5) |
| `blue.test.ts:424` | rejects unnecessary Blue authorization on a repay without collateral changes | 3 | Ported: `TestParityBlue/blue.test.ts:424` (1) |
| `blue.test.ts:436` | rejects an approval when Blue calldata does not pull tokens | 3 | Ported: `TestParityBlue/blue.test.ts:436` (1) |
| `blue.test.ts:448` | rejects native value when a Blue borrow does not pull collateral | 3 | Ported: `TestParityBlue/blue.test.ts:448` (2) |
| `blue.test.ts:473` | rejects zero exact amounts that match the unused calldata slot | 3 | Ported: `TestParityBlue/blue.test.ts:473` (2) |
| `blue.test.ts:490` | rejects an exact withdraw with a $rule | 3 | Ported: `TestParityBlue/blue.test.ts:490` (2) |
| `blue.test.ts:517` | distinguishes repay-all from an exact share repay | 3 | Ported: `TestParityBlue/blue.test.ts:517` (1) |
| `blue.test.ts:527` | rejects a larger maxRepayAssets on an exact-assets repay | 3 | Ported: `TestParityBlue/blue.test.ts:527` (2) |
| `blue.test.ts:549` | rounds down the referral fee when checking an exact-assets repay cap | 3 | Ported: `TestParityBlue/blue.test.ts:549` (3) |
| `blue.test.ts:604` | rejects malformed embedded Morpho authorization signatures | 3 | Ported: `TestParityBlue/blue.test.ts:604` (4) |
| `blue.test.ts:634` | rejects a standalone authorization before an embedded authorization | 3 | Ported: `TestParityBlue/blue.test.ts:634` (3) |
| `blue.test.ts:668` | rejects an altered refinance destination | 3 | Ported: `TestParityBlue/blue.test.ts:668` (1) |
| `blue.test.ts:678` | requires the liquidation buffer for $intent.action | 3 | Ported: `TestParityBlue/blue.test.ts:678` (3) |
| `blue.test.ts:754` | checks embedded funding permit kinds and ABI payloads | 3 | Ported: `TestParityBlue/blue.test.ts:754` (7) |
| `blue.test.ts:808` | accepts distinct idle and market sources, and rejects invalid reallocations | 3 | Ported: `TestParityBlue/blue.test.ts:808` (10) |
| `blue.test.ts:904` | checks both legs when a borrow or repay includes collateral | 3 | Ported: `TestParityBlue/blue.test.ts:904` (2) |
| `blue.test.ts:919` | rejects an unexpected $changed on a Blue collateral supply | 3 | Ported: `TestParityBlue/blue.test.ts:919` (5) |
| `blue.test.ts:979` | rejects unexpected $changed in a Blue borrow with no supply | 3 | Ported: `TestParityBlue/blue.test.ts:979` (2) |
| `blue.test.ts:1013` | rejects unexpected $changed in a Blue repay with no withdrawal | 3 | Ported: `TestParityBlue/blue.test.ts:1013` (2) |
| `blue.test.ts:1052` | rejects a zero-asset reallocation in $intent.action | 3 | Ported: `TestParityBlue/blue.test.ts:1052` (2) |
| `blue.test.ts:1121` | rejects encoded repayment %s for a Blue collateral-only withdrawal | 3 | Ported: `TestParityBlue/blue.test.ts:1121` (5) |
| `blue.test.ts:1165` | rejects a zero maximum in an all-mode Blue repay | 3 | Ported: `TestParityBlue/blue.test.ts:1165` (2) |
| `blue.test.ts:1192` | does not allow an all-withdrawal fee to pay another account | 3 | Ported: `TestParityBlue/blue.test.ts:1192` (1) |
| `blue.test.ts:1199` | does not allow a repay-only call to withdraw collateral | 3 | Ported: `TestParityBlue/blue.test.ts:1199` (1) |
| `blueSdk.test.ts:61` | requires the Base bundle for a Base Blue supply | 3 | Ported: `TestParityBlue/blueSdk.test.ts:61` (2) |
| `blueSdk.test.ts:83` | verifies an ERC-2612 permit encoded by the Blue supply builder | 3 | Ported: `TestParityBlue/blueSdk.test.ts:83` (1) |
| `blueSdk.test.ts:128` | verifies an SDK-built Blue withdraw in all mode and rejects $pair | 3 | Ported: `TestParityBlue/blueSdk.test.ts:128` (3) |
| `blueSdk.test.ts:159` | verifies an SDK-built Blue repay in all mode and rejects $pair | 3 | Ported: `TestParityBlue/blueSdk.test.ts:159` (4) |
| `blueSdk.test.ts:200` | rejects changing only the shares on an SDK-built exact-share repay | 3 | Ported: `TestParityBlue/blueSdk.test.ts:200` (2) |
| `blueSdk.test.ts:307` | verifies SDK-built $intent.action and rejects modified parameters | 3 | Ported: `TestParityBlue/blueSdk.test.ts:307` (57) |
| `checks.test.ts:26` | rejects %s as an unsigned decimal amount | 2 | Mapped: `TestCheckNumericBounds` |
| `checks.test.ts:39` | rejects invalid referral bps %j with a zero encoded fee | 2 | Mapped: `TestCheckNumericBounds` |
| `checks.test.ts:62` | rejects ETH sent to a token approval | 2 | Mapped: `TestApprovalAndCanonicalEncoding` |
| `checks.test.ts:71` | rejects a token transfer in place of an approval | 2 | Mapped: `TestApprovalAndCanonicalEncoding` |
| `checks.test.ts:85` | rejects trailing bytes only in nested calldata | 2 | Mapped: `TestApprovalAndCanonicalEncoding` |
| `checks.test.ts:100` | rejects calldata with %s | 2 | Mapped: `TestApprovalAndCanonicalEncoding` |
| `checks.test.ts:116` | rejects an odd hex suffix even when the decoded arguments are unchanged | 2 | Mapped: `TestApprovalAndCanonicalEncoding` |
| `midnight.test.ts:37` | rejects all mode and unknown units on collateral supplies | 4 | Ported: `TestParityMidnight/midnight.test.ts:37` (2) |
| `midnight.test.ts:52` | verifies %s and rejects another market | 4 | Mapped: `TestMidnightMarketVectors` |
| `midnight.test.ts:64` | rejects different amounts and fees | 4 | Ported: `TestParityMidnight/midnight.test.ts:64` (2) |
| `midnight.test.ts:77` | rejects $fixture.intent.action with the wrong repay type | 4 | Ported: `TestParityMidnight/midnight.test.ts:77` (4) |
| `midnight.test.ts:104` | rejects a full direct repay with asset cap %s | 4 | Ported: `TestParityMidnight/midnight.test.ts:104` (3) |
| `midnight.test.ts:141` | rejects a Midnight fee above the requested rate but within max_fee_bps | 4 | Ported: `TestParityMidnight/midnight.test.ts:141` (2) |
| `midnight.test.ts:152` | accepts no guard on $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:152` (3) |
| `midnight.test.ts:161` | checks approval tokens and authorization spender, owner, uniqueness and route | 4 | Ported: `TestParityMidnight/midnight.test.ts:161` (6) |
| `midnight.test.ts:217` | rejects a registered spender that is not the Midnight call target | 4 | Ported: `TestParityMidnight/midnight.test.ts:217` (2) |
| `midnight.test.ts:236` | requires an approval to cover the encoded pull on $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:236` (4) |
| `midnight.test.ts:258` | requires a zero approval to be immediately followed by a positive approval | 4 | Ported: `TestParityMidnight/midnight.test.ts:258` (4) |
| `midnight.test.ts:296` | rejects a Midnight prerequisite with %s authorization | 4 | Ported: `TestParityMidnight/midnight.test.ts:296` (2) |
| `midnight.test.ts:332` | checks the side, units, maker, and receiver of a borrow | 4 | Ported: `TestParityMidnight/midnight.test.ts:332` (4) |
| `midnight.test.ts:393` | rejects a Midnight borrow with $change | 4 | Ported: `TestParityMidnight/midnight.test.ts:393` (4) |
| `midnight.test.ts:443` | rejects a Midnight borrow with $change | 4 | Ported: `TestParityMidnight/midnight.test.ts:443` (2) |
| `midnight.test.ts:489` | rejects a Midnight lend with $change | 4 | Ported: `TestParityMidnight/midnight.test.ts:489` (2) |
| `midnight.test.ts:531` | rejects a direct repay with tampered flags, fills or collateral receiver | 4 | Ported: `TestParityMidnight/midnight.test.ts:531` (7) |
| `midnight.test.ts:630` | rejects a zero collateral receiver on a %s repay with a withdrawal | 4 | Ported: `TestParityMidnight/midnight.test.ts:630` (4) |
| `midnight.test.ts:703` | rejects loan-token approval when direct repay only withdraws collateral | 4 | Ported: `TestParityMidnight/midnight.test.ts:703` (3) |
| `midnight.test.ts:763` | checks zero and whole-balance collateral withdrawals on $intent.action ($intent.amount.unit) | 4 | Ported: `TestParityMidnight/midnight.test.ts:763` (20) |
| `midnight.test.ts:860` | accepts a zero collateral receiver on repay-only $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:860` (2) |
| `midnight.test.ts:911` | rejects a zero exact buy target | 4 | Ported: `TestParityMidnight/midnight.test.ts:911` (1) |
| `midnight.test.ts:936` | rejects a finite units target for a full direct repay | 4 | Ported: `TestParityMidnight/midnight.test.ts:936` (2) |
| `midnight.test.ts:965` | rejects an assets selector for a full direct repay even with a maximum target | 4 | Ported: `TestParityMidnight/midnight.test.ts:965` (1) |
| `midnight.test.ts:991` | rejects a market repay with a maximum target in $unit mode | 4 | Ported: `TestParityMidnight/midnight.test.ts:991` (2) |
| `midnight.test.ts:1030` | rejects an all-mode Midnight withdrawal via a units-target sell bundle | 4 | Ported: `TestParityMidnight/midnight.test.ts:1030` (1) |
| `midnight.test.ts:1058` | rejects a decoded Midnight offer-publication selector as a market action | 4 | Ported: `TestParityMidnight/midnight.test.ts:1058` (1) |
| `midnight.test.ts:1109` | rejects a selector for $wrong.intent.action when verifying $requested.intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:1109` (3) |
| `midnight.test.ts:1142` | rejects zero exact core withdrawal and collateral amounts | 4 | Ported: `TestParityMidnight/midnight.test.ts:1142` (2) |
| `midnight.test.ts:1192` | checks native value against the pull and verifies wrapped native token | 4 | Ported: `TestParityMidnight/midnight.test.ts:1192` (5) |
| `midnight.test.ts:1297` | rejects rate guards that cannot be verified from calldata | 4 | Ported: `TestParityMidnight/midnight.test.ts:1297` (2) |
| `midnight.test.ts:1314` | rejects wrong-side and unknown guards on %o | 4 | Ported: `TestParityMidnight/midnight.test.ts:1314` (8) |
| `midnight.test.ts:1334` | rejects a missing amount on $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:1334` (3) |
| `midnight.test.ts:1348` | rejects malformed collateral withdrawal requests with typed errors | 4 | Ported: `TestParityMidnight/midnight.test.ts:1348` (3) |
| `midnight.test.ts:1372` | rejects collateral withdrawals on a lend that carries a leftover list | 4 | Ported: `TestParityMidnight/midnight.test.ts:1372` (1) |
| `midnight.test.ts:1418` | rejects a fee on $intent.action that carries a leftover referral fee | 4 | Ported: `TestParityMidnight/midnight.test.ts:1418` (2) |
| `midnight.test.ts:1466` | rejects null collateral lists instead of treating them as omitted | 4 | Ported: `TestParityMidnight/midnight.test.ts:1466` (2) |
| `midnight.test.ts:1485` | rejects non-asset and non-unit amounts on direct withdraw | 4 | Ported: `TestParityMidnight/midnight.test.ts:1485` (1) |
| `midnight.test.ts:1496` | checks explicit guards against the primary target for $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:1496` (6) |
| `midnight.test.ts:1522` | rejects a lend whose pull exceeds the request's maximum assets | 4 | Ported: `TestParityMidnight/midnight.test.ts:1522` (1) |
| `midnight.test.ts:1545` | rejects a units-target borrow that weakens a caller-specified minimum receipt | 4 | Ported: `TestParityMidnight/midnight.test.ts:1545` (2) |
| `midnight.test.ts:1580` | requires the owner and receiver even for all withdrawals | 4 | Ported: `TestParityMidnight/midnight.test.ts:1580` (3) |
| `midnight.test.ts:1609` | rejects zero encoded units for all-mode $intent.action | 4 | Ported: `TestParityMidnight/midnight.test.ts:1609` (4) |
| `midnight.test.ts:1634` | rejects a zero all-mode leg in a core collateral withdrawal | 4 | Ported: `TestParityMidnight/midnight.test.ts:1634` (2) |
| `midnight.test.ts:1675` | rejects a reordered collateral list when checking the market hash | 4 | Ported: `TestParityMidnight/midnight.test.ts:1675` (1) |
| `midnight.test.ts:1701` | decodes all multicall legs and rejects a malformed leg | 4 | Ported: `TestParityMidnight/midnight.test.ts:1701` (2) |
| `midnight.test.ts:1717` | rejects a core collateral withdrawal with invalid $change | 4 | Ported: `TestParityMidnight/midnight.test.ts:1717` (4) |
| `midnightOffers.test.ts:87` | verifies %s | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:87` (6) |
| `midnightOffers.test.ts:94` | checks the group, market, amount and optional maker against the request | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:94` (6) |
| `midnightOffers.test.ts:122` | rejects lowering the encoded min_assets floor on a borrow take | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:122` (2) |
| `midnightOffers.test.ts:149` | rejects raising the encoded max_assets cap on a lend take | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:149` (2) |
| `midnightOffers.test.ts:184` | checks the group inside every fill | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:184` (1) |
| `midnightOffers.test.ts:214` | rejects $mutation in the same group with maker omitted | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:214` (2) |
| `midnightOffers.test.ts:277` | rejects a take with a rate guard that cannot be verified locally | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:277` (1) |
| `midnightOffers.test.ts:287` | rejects wrong-side and unknown guards on $0.intent.action | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:287` (6) |
| `midnightOffers.test.ts:306` | checks the cancel group, owner, amount and nested calldata | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:306` (5) |
| `midnightOffers.test.ts:351` | rejects invalid cancellation groups ($group) | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:351` (6) |
| `midnightOffers.test.ts:392` | rejects an empty batch of cancellations | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:392` (1) |
| `midnightOffers.test.ts:413` | rejects a non-cancellation call on the Midnight contract | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:413` (1) |
| `midnightOffers.test.ts:435` | checks a funding withdrawal against the backend's direct Blue withdrawal shape | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:435` (8) |
| `midnightOffers.test.ts:465` | rejects a different Blue selector for withdrawing funding | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:465` (1) |
| `midnightOffers.test.ts:481` | rejects invalid funding address %s | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:481` (2) |
| `midnightOffers.test.ts:502` | rejects a publication for %s with a typed unsupported error | 5 | Ported: `TestParityMidnightOffers/midnightOffers.test.ts:502` (4) |
| `midnightOffersSdk.test.ts:60` | rejects a group value that cannot be converted to a regex input | 5 | Ported: `TestParityMidnightOffers/midnightOffersSdk.test.ts:60` (1) |
| `midnightOffersSdk.test.ts:79` | rejects a single take fill with a different $field | 5 | Ported: `TestParityMidnightOffers/midnightOffersSdk.test.ts:79` (4) |
| `midnightOffersSdk.test.ts:147` | rejects the collateral take action without collateral supplies | 5 | Ported: `TestParityMidnightOffers/midnightOffersSdk.test.ts:147` (2) |
| `midnightOffersSdk.test.ts:171` | verifies a paid $intent.action and rejects a changed fee or recipient | 5 | Ported: `TestParityMidnightOffers/midnightOffersSdk.test.ts:171` (6) |
| `midnightOffersSdk.test.ts:317` | verifies SDK-built $intent.action and rejects modified parameters | 5 | Ported: `TestParityMidnightOffers/midnightOffersSdk.test.ts:317` (44) |
| `midnightSdk.test.ts:179` | rejects a fill from a different market in an SDK-built Midnight borrow | 4 | Ported: `TestParityMidnight/midnightSdk.test.ts:179` (1) |
| `midnightSdk.test.ts:234` | rejects reduceOnly=$reduceOnly for $action | 4 | Ported: `TestParityMidnight/midnightSdk.test.ts:234` (2) |
| `midnightSdk.test.ts:268` | verifies SDK-built $intent.action and rejects modified parameters | 4 | Ported: `TestParityMidnight/midnightSdk.test.ts:268` (69) |
| `vaultSdk.test.ts:78` | binds %s approvals to the requested asset | 2 | Ported: `TestParityVault/vaultSdk.test.ts:78` (12) |
| `vaultSdk.test.ts:179` | verifies a vault deposit with an SDK-encoded ERC-2612 permit | 2 | Ported: `TestParityVault/vaultSdk.test.ts:179` (1) |
| `vaultSdk.test.ts:199` | checks that a %s vault redemption's permit or approval covers the encoded shares | 2 | Ported: `TestParityVault/vaultSdk.test.ts:199` (12) |
| `vaultSdk.test.ts:343` | rejects a nonzero share amount on an exact-asset Vault withdrawal | 2 | Ported: `TestParityVault/vaultSdk.test.ts:343` (1) |
| `vaultSdk.test.ts:365` | rejects a zero requested Vault withdrawal in %s using the opposite slot | 2 | Ported: `TestParityVault/vaultSdk.test.ts:365` (2) |
| `vaultSdk.test.ts:399` | rejects a zero requested Vault withdrawal in %s with both encoded amounts zero | 2 | Ported: `TestParityVault/vaultSdk.test.ts:399` (4) |
| `vaultSdk.test.ts:444` | rejects a deposit whose requested and encoded assets are both zero | 2 | Ported: `TestParityVault/vaultSdk.test.ts:444` (1) |
| `vaultSdk.test.ts:470` | rejects a signed zero share permit for $intent.action | 2 | Ported: `TestParityVault/vaultSdk.test.ts:470` (4) |
| `vaultSdk.test.ts:524` | rejects a second encoded market on an in-kind redemption | 2 | Ported: `TestParityVault/vaultSdk.test.ts:524` (2) |
| `vaultSdk.test.ts:547` | rejects an unsigned nonzero share permit alongside a Vault approval | 2 | Ported: `TestParityVault/vaultSdk.test.ts:547` (2) |
| `vaultSdk.test.ts:581` | rejects the wrong Vault bundle selector for $current.intent.action | 2 | Ported: `TestParityVault/vaultSdk.test.ts:581` (4) |
| `vaultSdk.test.ts:603` | rejects disabled share-price ceilings for $intent.action | 2 | Ported: `TestParityVault/vaultSdk.test.ts:603` (12) |
| `vaultSdk.test.ts:636` | rejects a zero share-price floor for exact-asset force-withdraw | 2 | Ported: `TestParityVault/vaultSdk.test.ts:636` (4) |
| `vaultSdk.test.ts:665` | rejects a signed zero share permit $mode | 2 | Ported: `TestParityVault/vaultSdk.test.ts:665` (3) |
| `vaultSdk.test.ts:726` | rejects a deposit fee above the requested rate but within max_fee_bps | 2 | Ported: `TestParityVault/vaultSdk.test.ts:726` (2) |
| `vaultSdk.test.ts:738` | verifies SDK-built $intent.action and rejects modified parameters | 2 | Ported: `TestParityVault/vaultSdk.test.ts:738` (72) |
| `verifyCalldata.test.ts:40` | verifies %s and rejects another vault | 2 | Mapped: `TestVaultVectors` |
| `verifyCalldata.test.ts:53` | rejects non-string action $action without coercing it | 2 | JS-only: Go requires a string action in a typed intent; JavaScript coercion does not apply. |
| `verifyCalldata.test.ts:68` | rejects a non-asset unit on $intent.action even when the value matches | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:68` (4) |
| `verifyCalldata.test.ts:86` | rejects invalid call metadata %o | 2 | Mapped: `TestCalldataCallJSON` |
| `verifyCalldata.test.ts:96` | rejects a non-array call list %s | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:96` (3) |
| `verifyCalldata.test.ts:106` | rejects invalid withdrawal amount %o | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:106` (2) |
| `verifyCalldata.test.ts:117` | rejects malformed call entry %s with a typed error | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:117` (3) |
| `verifyCalldata.test.ts:130` | resolves the contract on the intended chain | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:130` (3) |
| `verifyCalldata.test.ts:142` | rejects invalid or unsupported chain ID $chain_id | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:142` (2) |
| `verifyCalldata.test.ts:161` | rejects a malformed or zero account: %s | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:161` (2) |
| `verifyCalldata.test.ts:175` | rejects a malformed or zero approval address: %s | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:175` (2) |
| `verifyCalldata.test.ts:191` | rejects a malformed vault before checking the call list | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:191` (1) |
| `verifyCalldata.test.ts:202` | rejects zero vault even when the intent and calldata agree | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:202` (1) |
| `verifyCalldata.test.ts:228` | rejects an invalid fee cap before checking the call list: %s | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:228` (3) |
| `verifyCalldata.test.ts:242` | checks the calldata rather than kind | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:242` (5) |
| `verifyCalldata.test.ts:268` | checks the assets and native value | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:268` (2) |
| `verifyCalldata.test.ts:280` | requires the original request to authorize the full native deposit value | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:280` (6) |
| `verifyCalldata.test.ts:326` | rejects a partial native value below the authorized full amount | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:326` (2) |
| `verifyCalldata.test.ts:351` | checks the embedded deposit permit encoding without verifying the signature | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:351` (11) |
| `verifyCalldata.test.ts:453` | rejects the contract's invalid 100% referral fee even at the maximum caller cap | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:453` (1) |
| `verifyCalldata.test.ts:477` | caps the referral fee and checks its destination | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:477` (2) |
| `verifyCalldata.test.ts:493` | requires an approval to the deposit bundle that covers the deposit | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:493` (6) |
| `verifyCalldata.test.ts:532` | checks both legs of a deposit approval reset | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:532` (5) |
| `verifyCalldata.test.ts:580` | binds the approval to the vault share token and the exit target for $intent.action | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:580` (9) |
| `verifyCalldata.test.ts:611` | requires the account to receive any referral fee on an all withdrawal | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:611` (2) |
| `verifyCalldata.test.ts:641` | checks recipients for nested calls without comparing an all-mode share count | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:641` (4) |
| `verifyCalldata.test.ts:687` | verifies SDK-built mixed adapter data and rejects a repeated empty route | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:687` (4) |
| `verifyCalldata.test.ts:737` | rejects a force-redeem leg on behalf of a different account | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:737` (2) |
| `verifyCalldata.test.ts:788` | rejects a native value on a force redemption | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:788` (1) |
| `verifyCalldata.test.ts:801` | rejects a force-redemption multicall without a deallocation | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:801` (1) |
| `verifyCalldata.test.ts:824` | rejects a force redemption with the wrong %s selector | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:824` (3) |
| `verifyCalldata.test.ts:863` | rejects invalid force-deallocation %s | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:863` (3) |
| `verifyCalldata.test.ts:914` | checks the market in an in-kind redemption | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:914` (2) |
| `verifyCalldata.test.ts:931` | reports a missing withdrawal amount as an amount.unit error | 2 | Ported: `TestParityVault/verifyCalldata.test.ts:931` (1) |
| `verifyCalldata.test.ts:948` | requires the action-specific intent fields | 2 | JS-only: TypeScript compile-time union and required-property assertions do not apply to Go's struct API. |
| `verifyCalldata.test.ts:958` | imports morpho-sdk through the ABI subpath only | 2 | JS-only: The JavaScript ABI subpath import restriction does not apply to the Go module. |
