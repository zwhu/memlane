"""Unit tests for shared IncrementalLogTailer rotation, truncation, and delta reads."""
import json
from pathlib import Path
import tempfile
import unittest

from log_tailer import IncrementalLogTailer


def _trace_line(req_hex: str, status: int = 502) -> str:
    return json.dumps({
        'schema_version': 1,
        'event': 'request_finished',
        'component': 'generation_http',
        'request_id': req_hex * 32,
        'status': status,
    }) + '\n'


class IncrementalLogTailerTests(unittest.TestCase):
    def test_incremental_append_truncation_rotation_and_inplace_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_path = root / 'official_service_dev.log'
            tailer = IncrementalLogTailer(tail_bytes=8192, max_rows=100)

            # Missing file returns empty list
            self.assertEqual(tailer.read_rows(log_path), [])

            # Initial write
            log_path.write_text(_trace_line('a', 502), encoding='utf-8')
            rows1 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows1], ['a' * 32])
            offset_after_1 = tailer._states[str(log_path)].offset

            # Unchanged poll hits cache without moving offset
            rows1_cached = tailer.read_rows(log_path)
            self.assertEqual(rows1_cached, rows1)
            self.assertEqual(tailer._states[str(log_path)].offset, offset_after_1)

            # Append second line -> incremental read advances offset
            with log_path.open('a', encoding='utf-8') as handle:
                handle.write(_trace_line('b', 503))
            rows2 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows2], ['a' * 32, 'b' * 32])
            offset_after_2 = tailer._states[str(log_path)].offset
            self.assertGreater(offset_after_2, offset_after_1)

            # Truncation (size < offset) resets state cleanly
            log_path.write_text(_trace_line('c', 500), encoding='utf-8')
            rows3 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows3], ['c' * 32])

            # In-place overwrite growing LARGER than previous offset (same inode) resets via anchor mismatch
            bigger_payload = _trace_line('d', 504) + _trace_line('e', 502)
            log_path.write_text(bigger_payload, encoding='utf-8')
            rows4 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows4], ['d' * 32, 'e' * 32])

            # Log rotation via rename + new file (new inode) resets cleanly
            rotated = root / 'official_service_dev.log.1'
            log_path.rename(rotated)
            log_path.write_text(_trace_line('f', 502) + _trace_line('1', 503) + _trace_line('2', 504), encoding='utf-8')
            rows5 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows5], ['f' * 32, '1' * 32, '2' * 32])

    def test_partial_trailing_line_and_symlink_rejection(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_path = root / 'stream.log'
            tailer = IncrementalLogTailer()

            # Write partial line without trailing newline
            partial = _trace_line('a', 502).rstrip('\n')
            log_path.write_text(partial, encoding='utf-8')
            rows_partial = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows_partial], ['a' * 32])
            self.assertEqual(tailer._states[str(log_path)].offset, 0)

            # Finish newline and append another line
            with log_path.open('a', encoding='utf-8') as handle:
                handle.write('\n' + _trace_line('b', 503))
            rows_complete = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows_complete], ['a' * 32, 'b' * 32])
            self.assertGreater(tailer._states[str(log_path)].offset, 0)

            # Symlink must fail closed with OSError
            outside = root / 'outside.log'
            outside.write_text(_trace_line('c', 502), encoding='utf-8')
            sym = root / 'symlink.log'
            sym.symlink_to(outside)
            with self.assertRaises(OSError):
                tailer.read_rows(sym)


if __name__ == '__main__':
    unittest.main()
