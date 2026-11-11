"""Persisted graph type projection; no model, graph DB, or user data."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('browsing', Path(__file__).resolve().parents[1] / 'engine/python/cognee_browsing.py')
browsing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(browsing)

class EntityTypes(unittest.TestCase):
    def test_only_explicit_typed_edges(self):
        nodes = {'type': {'type': 'EntityType', 'name': 'Package'},
                 'fake': {'type': 'Entity', 'name': 'Guessed'},
                 'bad': {'type': 'EntityType', 'name': 'x' * 1025}}
        edges = [('one', 'type', 'is_a'), ('one', 'type', 'is_a'),
                 ('two', 'type', 'contains'), ('one', 'fake', 'is_a'),
                 ('foreign', 'type', 'is_a'), ('one', 'bad', 'is_a')]
        self.assertEqual(browsing.entity_types(nodes, edges, {'one', 'two'}), {'one': ['Package']})
        self.assertEqual(browsing.entity_types(nodes, [], {'one'}), {})

if __name__ == '__main__':
    unittest.main()
