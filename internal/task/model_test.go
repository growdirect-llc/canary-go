package task

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateCreate(t *testing.T) {
	cases := []struct {
		name    string
		req     CreateTaskRequest
		wantErr bool
	}{
		{
			"good replenishment",
			CreateTaskRequest{TenantID: uuid.New(), TaskType: TypeReplenishment},
			false,
		},
		{
			"good receiving with explicit priority",
			CreateTaskRequest{TenantID: uuid.New(), TaskType: TypeReceiving, Priority: 2},
			false,
		},
		{
			"missing tenant",
			CreateTaskRequest{TaskType: TypeCycleCount},
			true,
		},
		{
			"invalid type",
			CreateTaskRequest{TenantID: uuid.New(), TaskType: "shrink"},
			true,
		},
		{
			"priority out of range",
			CreateTaskRequest{TenantID: uuid.New(), TaskType: TypeReceiving, Priority: 6},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateCreate(tc.req)
			if tc.wantErr && err == nil {
				t.Error("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("want nil, got %v", err)
			}
		})
	}
}

func TestValidateCreate_DefaultPriority(t *testing.T) {
	req := CreateTaskRequest{TenantID: uuid.New(), TaskType: TypeReceiving}
	clean, err := ValidateCreate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean.Priority != 3 {
		t.Errorf("want default priority 3, got %d", clean.Priority)
	}
}

func TestValidateTransition(t *testing.T) {
	cases := []struct {
		from, to string
		wantErr  bool
	}{
		{StatusQueued, StatusAssigned, false},
		{StatusQueued, StatusSkipped, false},
		{StatusQueued, StatusCancelled, false},
		{StatusAssigned, StatusInProgress, false},
		{StatusInProgress, StatusComplete, false},
		{StatusComplete, StatusVerified, false},
		// invalid
		{StatusQueued, StatusVerified, true},
		{StatusVerified, StatusQueued, true},
		{StatusComplete, StatusQueued, true},
		{StatusSkipped, StatusQueued, true},
	}
	for _, tc := range cases {
		err := ValidateTransition(tc.from, tc.to)
		if tc.wantErr && err == nil {
			t.Errorf("transition %s→%s: want error, got nil", tc.from, tc.to)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("transition %s→%s: want nil, got %v", tc.from, tc.to, err)
		}
	}
}
