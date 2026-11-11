package records

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSyntheticConnectionVerificationRequiresExactIdentityAndPayload(t *testing.T) {
	connection := "fa7a3c5b-659c-45d3-853b-dcf2a3415396"
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(connection+"\x00connection-check-12")).String()
	body := connectionVerificationTextPrefix + strings.ReplaceAll(connection, "-", "") + "12"
	source := AgentSource{Fragment: ConnectionVerificationFragment}
	if !IsSyntheticConnectionVerification(id, "agent:"+id, "raw", body, &source) {
		t.Fatal("exact handshake not recognized")
	}
	for _, test := range []struct{ name, memory, operation, origin, text, fragment string }{
		{"ordinary ID", uuid.NewString(), "agent:" + id, "raw", body, source.Fragment},
		{"ordinary operation", id, "user-save", "raw", body, source.Fragment},
		{"real knowledge", id, "agent:" + id, "knowledge", body, source.Fragment},
		{"prefix only", id, "agent:" + id, "raw", body + " genuine project content", source.Fragment},
		{"wrong source", id, "agent:" + id, "raw", body, "ordinary source"},
		{"noncanonical generation", id, "agent:" + id, "raw", connectionVerificationTextPrefix + strings.ReplaceAll(connection, "-", "") + "012", source.Fragment},
	} {
		t.Run(test.name, func(t *testing.T) {
			if IsSyntheticConnectionVerification(test.memory, test.operation, test.origin, test.text, &AgentSource{Fragment: test.fragment}) {
				t.Fatal("ordinary record classified as synthetic")
			}
		})
	}
}
