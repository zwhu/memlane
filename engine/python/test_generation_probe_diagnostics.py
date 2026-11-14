import json
import unittest

from pydantic import BaseModel, ConfigDict, ValidationError
from generation_probe import schema_error_summary, synthetic_response_shape, quality_mismatch_fields


class ProbeDiagnosticTests(unittest.TestCase):
    def test_schema_errors_keep_known_paths_and_types_without_values_or_extra_key_names(self):
        class Project(BaseModel):
            model_config = ConfigDict(extra='forbid')
            name: str
            contact: str | None

        class Extraction(BaseModel):
            model_config = ConfigDict(extra='forbid')
            project: Project
            ports: list[int]

        try:
            Extraction.model_validate({'project': {'name': ['private-body'], 'contact': None},
                                      'ports': ['private-number'], 'private-extra-key': 'private-value'})
        except ValidationError as error:
            summary = schema_error_summary(error)
        self.assertEqual(summary, [{'field': 'project.name', 'kind': 'string_type'},
                                   {'field': 'ports.0', 'kind': 'int_parsing'},
                                   {'field': 'other', 'kind': 'extra_forbidden'}])
        self.assertNotIn('private', json.dumps(summary))

    def test_unrelated_exceptions_are_not_serialized_and_cyclic_chains_stop(self):
        error = RuntimeError('private exception text')
        error.__cause__ = error
        self.assertEqual(schema_error_summary(error), [])

    def test_response_shape_distinguishes_schema_echo_without_recording_keys_or_values(self):
        for value, expected in [({'type': 'object', 'properties': {'private-key': {'type': 'string'}}}, 'schema_definition'),
                                ({'project': {'name': 'private-project'}}, 'data_object'),
                                ({'private-key': 'private-value'}, 'other_object'),
                                (['private-value'], 'non_object_json')]:
            self.assertEqual(synthetic_response_shape(json.dumps(value)), expected)
        self.assertEqual(synthetic_response_shape('private non-json'), 'not_json')

    def test_fact_diagnostics_report_only_expected_paths_without_incorrect_values(self):
        self.assertEqual(quality_mismatch_fields(
            {'project': {'name': 'private-wrong-name', 'contact': 'private-contact'}, 'ports': [999]},
            {'project': {'name': 'expected', 'contact': None}, 'ports': [43127, 43128]}),
            ['project.name', 'project.contact', 'ports.length', 'ports.0'])


if __name__ == '__main__':
    unittest.main()
