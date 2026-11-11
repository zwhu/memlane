package records

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const ConnectionVerificationFragment = "Memlane connection verification; synthetic only"
const connectionVerificationTextPrefix = "Memlane synthetic connection verification memlaneConnectionCheck"

// IsSyntheticConnectionVerification recognizes the exact production handshake
// identity and original save payload. Titles, naming prefixes and later usage
// on a real knowledge record are insufficient to classify it as synthetic.
func IsSyntheticConnectionVerification(memory, operationID, origin, text string, source *AgentSource) bool {
	if origin != "raw" || source == nil || source.Fragment != ConnectionVerificationFragment || source.Native != nil || source.Official != nil || source.History != nil || len(source.References) != 0 || !strings.HasPrefix(text, connectionVerificationTextPrefix) {
		return false
	}
	marker := strings.TrimPrefix(text, connectionVerificationTextPrefix)
	if len(marker) <= 32 {
		return false
	}
	connection, err := uuid.Parse(marker[:32])
	if err != nil || strings.ReplaceAll(connection.String(), "-", "") != marker[:32] {
		return false
	}
	generation, err := strconv.ParseInt(marker[32:], 10, 64)
	if err != nil || generation < 1 || strconv.FormatInt(generation, 10) != marker[32:] {
		return false
	}
	expected := uuid.NewSHA1(uuid.NameSpaceOID, []byte(connection.String()+"\x00connection-check-"+strconv.FormatInt(generation, 10))).String()
	return memory == expected && operationID == "agent:"+expected
}
