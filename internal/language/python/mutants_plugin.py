import json
import os
import time

import pytest

_output = os.environ.get("MUTANTS_COVERAGE")
_project = os.path.realpath(os.environ.get("MUTANTS_PROJECT", "."))
_coverage = None
_durations = {}

if _output:
    try:
        import coverage
    except ImportError:
        coverage = None
    if coverage is not None:
        _coverage = coverage.Coverage(
            data_file=None,
            config_file=False,
            source=[_project],
            omit=[os.path.join(_project, ".venv", "*"), "*/site-packages/*"],
        )
        _coverage.start()


@pytest.hookimpl(hookwrapper=True)
def pytest_load_initial_conftests(early_config, parser, args):
    # pytest-cov and this plugin cannot both trace the same process
    if hasattr(early_config.known_args_namespace, "no_cov"):
        early_config.known_args_namespace.no_cov = True
    yield


def pytest_configure(config):
    if _output and _coverage is None:
        with open(_output, "w") as output:
            json.dump({"coverage": False}, output)
        pytest.exit("mutants needs coverage.py in the environment of the tests", returncode=4)


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_protocol(item, nextitem):
    if _coverage is not None:
        _coverage.switch_context(item.nodeid)
    started = time.monotonic()
    yield
    _durations[item.nodeid] = time.monotonic() - started
    if _coverage is not None:
        _coverage.switch_context("")


@pytest.hookimpl(trylast=True)
def pytest_sessionfinish(session, exitstatus):
    if _coverage is None:
        return
    _coverage.stop()
    data = _coverage.get_data()
    files = {}
    for path in data.measured_files():
        real = os.path.realpath(path)
        if not real.startswith(_project + os.sep):
            continue
        _, statements, _, missing, _ = _coverage.analysis2(path)
        tests = {}
        for line, contexts in data.contexts_by_lineno(path).items():
            tests[str(line)] = sorted(context for context in contexts if context)
        files[os.path.relpath(real, _project)] = {"statements": statements, "missing": missing, "tests": tests}
    with open(_output, "w") as output:
        json.dump({"coverage": True, "files": files, "durations": _durations}, output)
