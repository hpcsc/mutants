import importlib.abc
import importlib.util
import os
import sys

_original = os.environ.get("MUTANTS_ORIGINAL")
_mutated = os.environ.get("MUTANTS_MUTATED")


class _Loader(importlib.abc.SourceLoader):
    def __init__(self, path, source):
        self._path = path
        self._source = source

    def get_filename(self, fullname):
        return self._path

    def get_data(self, path):
        if path == self._path:
            return self._source
        with open(path, "rb") as data:
            return data.read()


class _Finder(importlib.abc.MetaPathFinder):
    def __init__(self, path, source):
        self._path = path
        self._real = os.path.realpath(path)
        self._source = source

    def find_spec(self, fullname, path, target=None):
        for finder in sys.meta_path:
            if finder is self or not hasattr(finder, "find_spec"):
                continue
            spec = finder.find_spec(fullname, path, target)
            if spec is None:
                continue
            if spec.origin is None or os.path.realpath(spec.origin) != self._real:
                return spec
            return importlib.util.spec_from_file_location(
                fullname,
                spec.origin,
                loader=_Loader(spec.origin, self._source),
                submodule_search_locations=spec.submodule_search_locations,
            )
        return None


if _original and _mutated:
    with open(_mutated, "rb") as mutated:
        _source = mutated.read()
    try:
        compile(_source, _original, "exec")
    except SyntaxError as error:
        sys.stderr.write("mutants: the mutant does not compile: %s\n" % error)
        sys.stderr.flush()
        # the runner of mutants reads this exit code as NOT VIABLE
        os._exit(97)
    sys.meta_path.insert(0, _Finder(_original, _source))
