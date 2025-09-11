package controller

import (
	"domeal/middleware"
	"domeal/model"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

type BillWithPayPalLink struct {
	ID             int64  `json:"id"`
	TotalAmount    int64  `json:"total_amount"`
	PayPalMeLink   string `json:"paypal_me_link"`
	PayPalUsername string `json:"paypal_username"`
}

type BillingController struct {
	billingRepo model.BillingInterface
}

func NewBillingController(billingRepo model.BillingInterface) *BillingController {
	return &BillingController{
		billingRepo: billingRepo,
	}
}

func (c *BillingController) GetUserBill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		slog.Error("Invalid method", "method", r.Method)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	user, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		slog.Error("ミドルウェアからユーザー情報を取得できませんでした｡Cookieなどを確認すべき｡")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	bills, err := c.billingRepo.GetUserBillByUserID(ctx, int64(user.ID))
	if err != nil {
		slog.Error("Failed to get user bills",
			"error", err,
			"user_id", user.ID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// PayPal Meリンクを生成したレスポンスを作成
	var billsWithLinks []BillWithPayPalLink
	for _, bill := range bills {
		// PayPal Meリンクを生成
		paypalLink := fmt.Sprintf("https://www.paypal.com/paypalme/%s/%djpy", bill.PaypalMeUsername, bill.TotalAmount)

		billWithLink := BillWithPayPalLink{
			ID:             bill.ID,
			TotalAmount:    bill.TotalAmount,
			PayPalMeLink:   paypalLink,
			PayPalUsername: bill.PaypalMeUsername,
		}
		billsWithLinks = append(billsWithLinks, billWithLink)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(billsWithLinks); err != nil {
		slog.Error("Failed to encode user bills response",
			"error", err,
			"user_id", user.ID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	slog.Info("Successfully returned user bills with PayPal links",
		"user_id", user.ID,
		"bill_count", len(billsWithLinks))
}
