package api

import "testing"

func TestServiceBusinessImplementsCompletePort(t *testing.T) {
	var business Business = NewServiceBusiness(nil, ServiceBusinessConfig{})
	if business == nil {
		t.Fatal("NewServiceBusiness returned nil")
	}
}
