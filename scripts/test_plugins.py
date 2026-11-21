"""Keeps HawkEye's agent plugins (plugins/hawkeye for Claude Code, plugins/hawkeye-cursor, plugins/hawkeye-codex) in step.

The plugins ship the same skill and icon, copied into each folder because each agent installs only its own folder. They also release together under
one version (docs/directory-submission.md, "Releasing a plugin change"). These tests fail when a copy drifts, a version is raised in one manifest
only, or a marketplace entry points at a folder that isn't a plugin for that agent.
"""
import filecmp
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
PLUGINS = ROOT / "plugins"
CLAUDE = PLUGINS / "hawkeye"
CURSOR = PLUGINS / "hawkeye-cursor"
CODEX = PLUGINS / "hawkeye-codex"
MCP_URL = "https://hawkeye.observer/mcp"


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def shared_files(plugin):
    """Every file under skills/ and assets/, relative to the plugin folder."""
    return sorted(p.relative_to(plugin) for d in ("skills", "assets") for p in (plugin / d).rglob("*") if p.is_file())


class PluginsInSyncTest(unittest.TestCase):
    def test_skills_and_assets_match_the_claude_plugin(self):
        expected = shared_files(CLAUDE)
        self.assertIn(pathlib.Path("skills/set-up-monitoring/SKILL.md"), expected)
        for plugin in (CURSOR, CODEX):
            with self.subTest(plugin=plugin.name):
                self.assertEqual(shared_files(plugin), expected)
                for rel in expected:
                    self.assertTrue(filecmp.cmp(CLAUDE / rel, plugin / rel, shallow=False),
                                    f"{plugin.name}/{rel} differs from hawkeye/{rel}: copy it from plugins/hawkeye")

    def test_one_version_for_every_plugin(self):
        versions = {
            "claude": load(CLAUDE / ".claude-plugin/plugin.json")["version"],
            "cursor": load(CURSOR / ".cursor-plugin/plugin.json")["version"],
            "codex": load(CODEX / ".codex-plugin/plugin.json")["version"],
        }
        self.assertEqual(len(set(versions.values())), 1, f"plugin versions differ: {versions}")

    def test_every_plugin_uses_the_production_mcp_server(self):
        for path in (CLAUDE / ".mcp.json", CURSOR / "mcp.json", CODEX / ".mcp.json"):
            with self.subTest(path=str(path.relative_to(ROOT))):
                self.assertEqual(load(path)["mcpServers"]["hawkeye"]["url"], MCP_URL)

    def test_cursor_key_comes_from_the_plugin_variable(self):
        manifest = load(CURSOR / ".cursor-plugin/plugin.json")
        self.assertIn("HAWKEYE_API_KEY", manifest["variables"]["required"])
        header = load(CURSOR / "mcp.json")["mcpServers"]["hawkeye"]["headers"]["Authorization"]
        self.assertEqual(header, "Bearer ${HAWKEYE_API_KEY}")

    def test_codex_oauth_client_is_registered(self):
        # The client ID comes from `oauthclient create -name Codex -public` in production (docs/directory-submission.md). A placeholder would
        # publish a plugin whose sign-in always fails, so this fails until the real ID is in place.
        oauth = load(CODEX / ".mcp.json")["mcpServers"]["hawkeye"]["oauth"]
        self.assertRegex(oauth["clientId"], r"^hkc_[A-Za-z0-9_-]+$")
        self.assertEqual(oauth["callbackUrl"], f"http://127.0.0.1:{oauth['callbackPort']}/callback")

    def test_marketplaces_point_at_their_plugins(self):
        claude = load(ROOT / ".claude-plugin/marketplace.json")["plugins"][0]["source"]
        self.assertTrue((ROOT / claude / ".claude-plugin/plugin.json").is_file())

        cursor = load(ROOT / ".cursor-plugin/marketplace.json")
        root = cursor.get("metadata", {}).get("pluginRoot", "")
        self.assertTrue((ROOT / root / cursor["plugins"][0]["source"] / ".cursor-plugin/plugin.json").is_file())

        codex = load(ROOT / ".agents/plugins/marketplace.json")["plugins"][0]["source"]
        self.assertTrue(codex["path"].startswith("./"))
        self.assertTrue((ROOT / codex["path"] / ".codex-plugin/plugin.json").is_file())

    def test_codex_listing_fits_the_store_limits(self):
        interface = load(CODEX / ".codex-plugin/plugin.json")["interface"]
        self.assertLessEqual(len(interface["displayName"]), 30)
        self.assertLessEqual(len(interface["shortDescription"]), 30)
        for key in ("composerIcon", "logo"):
            self.assertTrue((CODEX / interface[key]).is_file(), key)

    def test_plugin_names_are_lowercase_kebab_case(self):
        for path in (CLAUDE / ".claude-plugin/plugin.json", CURSOR / ".cursor-plugin/plugin.json", CODEX / ".codex-plugin/plugin.json"):
            with self.subTest(path=str(path.relative_to(ROOT))):
                self.assertRegex(load(path)["name"], r"^[a-z0-9]+(-[a-z0-9]+)*$")


if __name__ == "__main__":
    unittest.main()
