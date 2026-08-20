package components

import "html/template"

// StatusBadge is a small colored label for an entity's status. Kind is one
// of "success", "danger", "muted", "accent", or "" for the default info
// styling, matching the .badge-* classes in app.css.
type StatusBadge struct {
	Label string
	Kind  string
}

// AdminTable is a generic rows-and-columns table, used by every admin list
// page so they share one markup/style instead of each hand-building a
// <table>. Cells are template.HTML so callers can embed links, forms, and
// StatusBadge output; plain text must be passed through template.HTMLEscapeString
// or template.HTML(template.HTMLEscapeString(s)) by the caller.
type AdminTable struct {
	Columns []string
	Rows    [][]template.HTML
}

// FilterField is one control in a FilterForm.
type FilterField struct {
	Name    string
	Label   string
	Value   string
	Options []FilterOption // empty means a text input instead of a select
}

// FilterOption is one <option> in a FilterField select.
type FilterOption struct {
	Value    string
	Label    string
	Selected bool
}

// FilterForm is a GET search/status filter bar, shared by admin list pages.
type FilterForm struct {
	Action string
	Fields []FilterField
}

// SellerWalletData backs the seller payout-wallet settings page.
type SellerWalletData struct {
	Enabled      bool
	CSRF         string
	CooldownText string
	Wallets      []SellerWalletRow
}

// SellerWalletRow is one confirmed wallet on file.
type SellerWalletRow struct {
	Asset   string
	Network string
	Status  string
}

// SellerWalletPage renders the seller payout-wallet settings page.
func SellerWalletPage(d SellerWalletData) (template.HTML, error) {
	return execute("sellerwallet", d)
}

// SellerPayoutsData backs the seller payout-request page.
type SellerPayoutsData struct {
	Enabled  bool
	CSRF     string
	Paused   bool
	Eligible []SellerPayoutEligibleWallet
	Payouts  []SellerPayoutRow
}

// SellerPayoutEligibleWallet is a confirmed, payout-eligible wallet a seller
// can request a payout against.
type SellerPayoutEligibleWallet struct {
	Network       string
	Asset         string
	WalletID      int64
	AvailableText string
}

// SellerPayoutRow is one row of a seller's payout history.
type SellerPayoutRow struct {
	CreatedAt   string
	AmountText  string
	Network     string
	Asset       string
	StatusBadge StatusBadge
}

// SellerPayoutsPage renders the seller payout-request page.
func SellerPayoutsPage(d SellerPayoutsData) (template.HTML, error) {
	return execute("sellerpayouts", d)
}

// BTCPayInvoiceData backs the zero-JS BTCPay invoice polling page.
type BTCPayInvoiceData struct {
	StatusText  string
	OrderID     int64
	CheckoutURL string
}

// BTCPayInvoicePage renders the BTCPay invoice polling page.
func BTCPayInvoicePage(d BTCPayInvoiceData) (template.HTML, error) {
	return execute("btcpayinvoice", d)
}

// EVMDepositData backs the zero-JS EVM stablecoin deposit polling page.
type EVMDepositData struct {
	StatusText    string
	AmountText    string
	ChainName     string
	QR            template.HTML
	Address       string
	Confirmations int
	OrderID       int64
}

// EVMDepositPage renders the EVM stablecoin deposit polling page.
func EVMDepositPage(d EVMDepositData) (template.HTML, error) {
	return execute("evmdeposit", d)
}

// AdminBalanceRow is one row of the platform balances table.
type AdminBalanceRow struct {
	Kind     string
	Currency string
	Amount   string
}

// AdminDeadJobRow is one dead-lettered webhook job needing manual review.
type AdminDeadJobRow struct {
	ID        int64
	Attempts  string
	LastError string
	Updated   string
}

// AdminPaymentsData backs the admin platform-payments overview page.
type AdminPaymentsData struct {
	CSRF     string
	Balances []AdminBalanceRow
	DeadJobs []AdminDeadJobRow
}

// AdminPaymentsPage renders the admin platform-payments overview page.
func AdminPaymentsPage(d AdminPaymentsData) (template.HTML, error) {
	return execute("adminpayments", d)
}

// OnChainPaymentDetail is the BTCPay/EVM-specific detail block appended to
// an order's payment page.
type OnChainPaymentDetail struct {
	NoProvider     bool
	LiveCheckError string
	LiveStatus     string
	ChargeLabel    string
	ChargeRef      string
	FailureCode    string
	FailureReason  string
	AttemptsError  string
	Attempts       []PaymentAttemptRow
}

// HasFailure reports whether either failure field is set, so the template
// doesn't need an explicit two-field 'or' check.
func (d OnChainPaymentDetail) HasFailure() bool {
	return d.FailureCode != "" || d.FailureReason != ""
}

// PaymentAttemptRow is one recorded provider status observation.
type PaymentAttemptRow struct {
	When           string
	Status         string
	FailureCode    string
	FailureMessage string
}

// AdminOrderPaymentsData backs the admin order-payments detail page.
type AdminOrderPaymentsData struct {
	OrderID          int64
	CSRF             string
	HasIntent        bool
	Provider         string
	ProviderRef      string
	Status           string
	AmountText       string
	ShowRefund       bool
	RefundAmountText string
	OnChain          *OnChainPaymentDetail
}

// AdminOrderPaymentsPage renders the admin order-payments detail page.
func AdminOrderPaymentsPage(d AdminOrderPaymentsData) (template.HTML, error) {
	return execute("adminorderpayments", d)
}

// AdminPayoutRow is one payout listed under an admin payout status group.
type AdminPayoutRow struct {
	ID         int64
	SellerID   int64
	AmountText string
	Network    string
	Asset      string
	ActionKind string // "approve", "complete", or "" for none
}

// AdminPayoutGroup is one status section of the admin payouts page.
type AdminPayoutGroup struct {
	Status string
	Rows   []AdminPayoutRow
}

// AdminPayoutsData backs the admin payouts page.
type AdminPayoutsData struct {
	CSRF        string
	Paused      bool
	PauseLabel  string
	PauseAction string
	Groups      []AdminPayoutGroup
}

// AdminPayoutsPage renders the admin payouts page.
func AdminPayoutsPage(d AdminPayoutsData) (template.HTML, error) {
	return execute("adminpayouts", d)
}

// AdminHomePage renders the admin console landing page.
func AdminHomePage() (template.HTML, error) {
	return execute("adminhome", nil)
}

// AdminListData is the generic shell for admin list pages: an optional
// lead block (search/filter form, static links), one AdminTable, an
// optional trailing block, and a back link.
type AdminListData struct {
	Title     string
	Lead      template.HTML
	Table     AdminTable
	Extra     template.HTML
	BackHref  string
	BackLabel string
}

// AdminListPage renders a generic admin list page.
func AdminListPage(d AdminListData) (template.HTML, error) {
	return execute("adminlist", d)
}

// moderationFilterData backs the "moderationfilter" partial.
type moderationFilterData struct {
	Base    string
	Current string
	States  []string
}

// ModerationFilterBar renders the pending/approved/rejected state filter
// links shared by the gig/media/review moderation queues.
func ModerationFilterBar(base, current string) (template.HTML, error) {
	return execute("moderationfilter", moderationFilterData{
		Base: base, Current: current, States: []string{"pending", "approved", "rejected"},
	})
}

// actionFormData backs the "moderationactions"/"hideaction" partials.
type actionFormData struct {
	Action string
	CSRF   string
}

// ModerationActions renders the approve/reject button pair posted to action.
func ModerationActions(action, csrf string) (template.HTML, error) {
	return execute("moderationactions", actionFormData{Action: action, CSRF: csrf})
}

// HideAction renders a single "Hide" button form posted to action.
func HideAction(action, csrf string) (template.HTML, error) {
	return execute("hideaction", actionFormData{Action: action, CSRF: csrf})
}

// FilterFormHTML renders a GET search/filter bar.
func FilterFormHTML(f FilterForm) (template.HTML, error) {
	return execute("filterform", f)
}

// AdminDisputeEvidence is one dispute-evidence attachment.
type AdminDisputeEvidence struct {
	ID       int64
	FileName string
}

// AdminDisputeDetailData backs the admin dispute detail page.
type AdminDisputeDetailData struct {
	ID            int64
	OrderID       int64
	OpenedBy      int64
	Reason        string
	Status        string
	Decision      string
	Evidence      []AdminDisputeEvidence
	CSRF          string
	InternalNotes string
	Open          bool
}

// AdminDisputeDetailPage renders the admin dispute detail page.
func AdminDisputeDetailPage(d AdminDisputeDetailData) (template.HTML, error) {
	return execute("admindisputedetail", d)
}

// AdminLedgerAdjustData backs the admin ledger adjustment form.
type AdminLedgerAdjustData struct {
	CSRF         string
	AccountKinds []string
}

// AdminLedgerAdjustPage renders the admin ledger adjustment form.
func AdminLedgerAdjustPage(d AdminLedgerAdjustData) (template.HTML, error) {
	return execute("adminledgeradjust", d)
}

// AdminCategoryRow is one editable browse category.
type AdminCategoryRow struct {
	ID          int64
	Slug        string
	Name        string
	Description string
	Position    int
}

// AdminTagRow is one seller-supplied tag with its usage count.
type AdminTagRow struct {
	ID       int64
	Name     string
	GigCount int
}

// AdminCategoriesTagsData backs the admin categories & tags page.
type AdminCategoriesTagsData struct {
	CSRF       string
	Categories []AdminCategoryRow
	Tags       []AdminTagRow
}

// AdminCategoriesTagsPage renders the admin categories & tags page.
func AdminCategoriesTagsPage(d AdminCategoriesTagsData) (template.HTML, error) {
	return execute("admincategoriestags", d)
}

// AdminSettingRow is one platform setting.
type AdminSettingRow struct {
	Key     string
	Value   string
	Updated string
}

// AdminSettingsData backs the admin settings page.
type AdminSettingsData struct {
	CSRF     string
	Settings []AdminSettingRow
}

// AdminSettingsPage renders the admin settings page.
func AdminSettingsPage(d AdminSettingsData) (template.HTML, error) {
	return execute("adminsettings", d)
}

// AdminAttemptRow is one payment attempt in an order's timeline.
type AdminAttemptRow struct {
	When    string
	Status  string
	Failure string
}

// AdminWebhookEventRow is one webhook event matched to an order.
type AdminWebhookEventRow struct {
	EventID   string
	EventType string
	Status    string
	Attempts  int
}

// AdminOrderTimelineData backs the admin order timeline page.
type AdminOrderTimelineData struct {
	OrderID   int64
	HasIntent bool
	IntentID  int64
	Provider  string
	Status    string
	Created   string
	Attempts  []AdminAttemptRow
	Events    []AdminWebhookEventRow
}

// AdminOrderTimelinePage renders the admin order timeline page.
func AdminOrderTimelinePage(d AdminOrderTimelineData) (template.HTML, error) {
	return execute("adminordertimeline", d)
}

// userStatusActionData backs the "userstatusaction" partial.
type userStatusActionData struct {
	UserID  int64
	Action  string
	CSRF    string
	Suspend bool
}

// SuspendUserAction renders the suspend-user action form.
func SuspendUserAction(userID int64, action, csrf string) (template.HTML, error) {
	return execute("userstatusaction", userStatusActionData{UserID: userID, Action: action, CSRF: csrf, Suspend: true})
}

// RestoreUserAction renders the restore-user action form.
func RestoreUserAction(action, csrf string) (template.HTML, error) {
	return execute("userstatusaction", userStatusActionData{Action: action, CSRF: csrf, Suspend: false})
}
