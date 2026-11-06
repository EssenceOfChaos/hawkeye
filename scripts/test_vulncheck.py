"""Tests for vulncheck.py. The gate must fail on anything it was not told about, so most of these check that an exception does not reach too far."""
import datetime
import json
import unittest

import vulncheck

MODULE = "github.com/labstack/echo/v4"
TODAY = datetime.date(2026, 11, 6)
RULE = vulncheck.Accepted(MODULE, "v4.16.3", datetime.date(2027, 1, 31), "not affected")
CONFIG = {"config": {"scanner_name": "govulncheck"}}


def finding(osv, module=MODULE, version="v4.16.3", function="ServeHTTP"):
    frame = {"module": module, "version": version}
    if function:
        frame["function"] = function
    return {"finding": {"osv": osv, "trace": [frame]}}


class EvaluateTest(unittest.TestCase):
    def check(self, *found, today=TODAY):
        return vulncheck.evaluate([CONFIG, *found], {"GO-1": RULE}, today)

    def test_accepted_finding_is_excused(self):
        self.assertEqual(self.check(finding("GO-1")), ([], ["GO-1"], []))

    def test_later_versions_stay_excused(self):
        self.assertEqual(self.check(finding("GO-1", version="v4.17.0"))[0], [])

    def test_unknown_finding_fails(self):
        self.assertEqual(self.check(finding("GO-1"), finding("GO-2")), (["GO-2"], ["GO-1"], []))

    def test_older_version_fails(self):
        self.assertEqual(self.check(finding("GO-1", version="v4.16.2"))[0], ["GO-1"])

    def test_prerelease_and_pseudo_versions_fail(self):
        for version in ("v4.16.3-rc1", "v4.16.4-0.20261101000000-abcdefabcdef", "", None):
            self.assertEqual(self.check(finding("GO-1", version=version))[0], ["GO-1"], version)

    def test_another_module_fails(self):
        self.assertEqual(self.check(finding("GO-1", module="example.com/other"))[0], ["GO-1"])

    def test_exception_lapses_after_its_review_date(self):
        self.assertEqual(self.check(finding("GO-1"), today=datetime.date(2027, 1, 31))[0], [])
        self.assertEqual(self.check(finding("GO-1"), today=datetime.date(2027, 2, 1))[0], ["GO-1"])

    def test_a_module_we_do_not_call_is_not_a_failure(self):
        self.assertEqual(self.check(finding("GO-2", function=None))[0], [])

    def test_reports_an_exception_that_is_no_longer_needed(self):
        self.assertEqual(self.check(), ([], [], ["GO-1"]))

    def test_a_scan_without_results_is_an_error(self):
        with self.assertRaises(ValueError):
            vulncheck.evaluate([], {"GO-1": RULE}, TODAY)


class ParseStreamTest(unittest.TestCase):
    def test_reads_a_stream_of_objects(self):
        text = json.dumps(CONFIG, indent=2) + "\n" + json.dumps(finding("GO-1"), indent=2) + "\n"
        self.assertEqual(vulncheck.parse_stream(text), [CONFIG, finding("GO-1")])

    def test_cut_off_output_is_an_error(self):
        with self.assertRaises(ValueError):
            vulncheck.parse_stream('{"config": {')


class AcceptedListTest(unittest.TestCase):
    def test_every_exception_explains_itself_and_can_lapse(self):
        for osv, rule in vulncheck.ACCEPTED.items():
            self.assertTrue(rule.reason, osv)
            self.assertLess(rule.review_by, datetime.date(2028, 1, 1), osv)


if __name__ == "__main__":
    unittest.main()
