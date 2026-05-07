// internal/arts/constants.go
// ARTS POSLOG field constants used across all transaction parsing.
package arts

const (
	TransactionTypeSale   = "SALE"
	TransactionTypeRefund = "REFUND"
	TransactionTypeVoid   = "VOID"

	TenderTypeCash     = "CASH"
	TenderTypeCard     = "CARD"
	TenderTypeGiftCard = "GIFT_CARD"
	TenderTypeOther    = "OTHER"

	SourceSquare       = "square"
	SourceCounterpoint = "counterpoint"
	SourceClover       = "clover"
)
