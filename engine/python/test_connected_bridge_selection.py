import unittest
from types import SimpleNamespace
from unittest.mock import patch
from project_policy import PolicyError
from agent_bridge_runtime import require_connected_bridge

class ConnectedBridgeSelectionTests(unittest.TestCase):
    def test_connections_gate_agents_independently_of_model_validation(self):
        for state, allowed in [('configured_pending_new_session',True),('prepared',False),('requires_review',False),('environment_receipt_unconfirmed',False),('not_connected',False)]:
            with self.subTest(state=state), patch('agent_connections.Connections') as connections, patch('agent_bridge_runtime.check_bridge_runtime') as authentication:
                connections.return_value.view.return_value={'connections':[{'kind':'pi','status':state}]}
                if allowed:
                    require_connected_bridge(SimpleNamespace(),{}, {'mode':'agent_bridge','bridge_kind':'pi'})
                    authentication.assert_called_once()
                else:
                    with self.assertRaises(PolicyError) as error:
                        require_connected_bridge(SimpleNamespace(),{}, {'mode':'agent_bridge','bridge_kind':'pi'})
                    self.assertEqual(error.exception.code,'generation_agent_not_connected')
                    authentication.assert_not_called()
