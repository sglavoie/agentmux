"""Render the real stand-in prompts without launching agents; use Go's parser."""

import ast
import contextlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[3]


class DemoPromptTests(unittest.TestCase):
    def test_rendered_prompts_are_blocked_with_real_options_and_command_story(self):
        source = REPO / "scripts/demo/agent.py"
        # Compile only the rendering functions: no top-level transcript writes,
        # credential access, termios changes, or managed hooks during this test.
        functions = ast.Module(body=[node for node in ast.parse(source.read_text()).body
                                    if isinstance(node, ast.FunctionDef)
                                    and node.name in ("heading", "waiting")], type_ignores=[])
        screens = {}
        for harness in ("claude", "codex", "copilot"):
            namespace = {"harness": harness, "project": "demo", "hook": lambda *a, **k: None}
            exec(compile(functions, str(source), "exec"), namespace)
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                namespace["waiting"]()
            screens[harness] = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", output.getvalue())
            self.assertIn("Scripted demo data", screens[harness])
        # A temporary package inside the module can use its internal parser.
        with tempfile.TemporaryDirectory(prefix=".demo-prompt-check-", dir=REPO) as directory:
            helper = Path(directory) / "main.go"
            helper.write_text('''package main
import (
 "encoding/json"
 "fmt"
 "os"
 "strings"
 "github.com/sglavoie/agentmux/internal/screenstate"
)
func main() {
 var screens map[string]string
 if err := json.NewDecoder(os.Stdin).Decode(&screens); err != nil { panic(err) }
 for harness, screen := range screens {
  adapter, ok := screenstate.For(harness)
  if !ok || adapter.Classify(screen).State != screenstate.Blocked { panic(harness + ": not blocked") }
  prompt, ok := screenstate.ParsePrompt(strings.Split(screen, "\\n"))
  if !ok || !strings.HasSuffix(prompt.Question, "?") ||
   prompt.Summary != "$ make check   (scripted; no command is executed)" ||
   len(prompt.Options) != 2 || prompt.Options[0].Key != "1" ||
   prompt.Options[0].Label != "Yes" || !prompt.Options[0].Selected ||
   prompt.Options[1].Key != "2" || prompt.Options[1].Label != "No" || prompt.Options[1].Selected {
   panic(fmt.Sprintf("%s: %+v", harness, prompt))
  }
 }
}
''')
            environment = {k: v for k, v in os.environ.items()
                           if k not in ("AMX_RUNTIME_SESSION_ID", "TMUX", "TMUX_PANE")}
            result = subprocess.run(["go", "run", str(helper)], cwd=REPO, env=environment,
                                    input=json.dumps(screens), capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
