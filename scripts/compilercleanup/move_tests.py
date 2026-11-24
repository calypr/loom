#!/usr/bin/env python3
"""Move compiler root test declarations and their fixtures from the Phase 1 tree."""

from collections import defaultdict
from dataclasses import dataclass
import json
from pathlib import Path
import re
import subprocess
from tempfile import TemporaryDirectory

from owners import get_owner

ROOT = Path("internal/dataframe/compiler")
FIXTURES = Path("internal/testkit/compilerfixtures")
MODULE = "github.com/calypr/loom/"
ASSERTIONS = {
    "assertCategoryScanBindVarsMatchQuery", "assertCurrentUnpivotAuthoredSlots",
    "categoryScanIdentifierByte", "compileAndFindCellTrace", "traceContributions",
    "containsBindValue", "executeRowLineageOracleQuery", "lineageContributorRows",
    "groupRowsMembership", "relatedExpandRowID", "sortedAnyStrings",
}
OUTPUT_COMPILERS = {
    "compileConstructionOutputQuery", "compileConstructionOutputQueryWithBindings",
    "compileCurrentConstructionOracle", "compileReshapeOracle",
    "executeConstructionOutput", "executeConstructionOutputError",
}
EXTRA_FIXTURES = {
    "relatedCountEdge", "buildGenericPhysicalPlan", "buildGenericPhysicalPlanWithContext",
    "testSemanticField", "testSemanticFieldWithFallback",
    "openConstructionReshapeArango", "insertConstructionReshapeRows",
}


@dataclass
class Declaration:
    source: Path
    names: list
    start: int
    end: int
    uses: set
    body: str

    @property
    def test(self):
        return self.names[0].startswith(("Test", "Benchmark", "Fuzz", "Example"))


def go_declarations(paths):
    with TemporaryDirectory() as scratch:
        tool = Path(scratch) / "decls"
        subprocess.run([
            "go", "build", "-o", str(tool), str(Path(__file__).with_name("decls.go"))
        ], check=True)
        return json.loads(subprocess.check_output([str(tool), *map(str, paths)]))



def import_lines(source):
    block = re.search(r"^import \(\n(.*?)^\)", source, re.M | re.S)
    if block:
        return [line.strip() for line in block[1].splitlines() if '"' in line]
    return re.findall(r'^import ("[^"]+")$', source, re.M)


def file_text(package, imports, declarations):
    return (f"package {package}\n\nimport (\n" +
            "\n".join(sorted(set(imports))) + "\n)\n\n" +
            "\n\n".join(declarations) + "\n")


def rename(text, names):
    if not names:
        return text
    pattern = r"(?<![.\w])(?:" + "|".join(re.escape(name) for name in sorted(names)) + r")\b"
    return re.sub(pattern, lambda match: names[match[0]], text)


def output_directory(owner):
    owner = owner.removesuffix("@internal")
    return ROOT.parent / owner[1:] if owner.startswith("/") else ROOT / owner


def package_name(owner):
    name = output_directory(owner).name
    return name if owner.endswith("@internal") else name + "_test"


def source_stem(path):
    return path.name.removesuffix("_test.go")


def expanded_inputs(declarations, index, source):
    owners = defaultdict(set)
    for key, decl in enumerate(declarations):
        if not decl.test:
            continue
        owner = get_owner(source_stem(decl.source), decl.names[0])
        queue = [key]
        seen = set()
        while queue:
            found = queue.pop()
            if found in seen:
                continue
            seen.add(found)
            owners[found].add(owner)
            queue.extend(index[name] for name in declarations[found].uses if name in index)
    for key, decl in enumerate(declarations):
        if not owners[key]:
            owners[key].add("root")
    for key, decl in enumerate(declarations):
        if decl.test and len(owners[key]) != 1:
            raise ValueError(f"test used by multiple owners: {decl.names[0]}")
        if "root" in owners[key]:
            continue
        private = {name for name in decl.uses & source.keys() if name[0].islower()}
        if owners[key] == {"render/aql@internal"}:
            private.discard("categoryLimitBind")
        if private:
            raise ValueError(f"{decl.names[0]} needs root private names {sorted(private)}")
    return owners


def place_tests():
    files = sorted(ROOT.glob("*_test.go"))
    production = sorted(path for path in ROOT.glob("*.go") if path not in files)
    raw = go_declarations(files + production)
    declarations = []
    source = {}
    index = {}
    for path in files + production:
        contents = path.read_bytes()
        for row in raw[str(path)] or []:
            if path in production:
                for name in row["Names"]:
                    source[name] = contents[row["Start"]:row["End"]].decode()
                continue
            decl = Declaration(path, row["Names"], row["Start"], row["End"],
                               set(row["Uses"]), contents[row["Start"]:row["End"]].decode())
            key = len(declarations)
            for name in decl.names:
                if name in index:
                    raise ValueError(f"duplicate test declaration: {name}")
                index[name] = key
            declarations.append(decl)

    owners = expanded_inputs(declarations, index, source)
    shared = set()
    for key, decl in enumerate(declarations):
        names = set(decl.names)
        if not decl.test and not names & ASSERTIONS and (
                len(owners[key]) > 1 or names & EXTRA_FIXTURES):
            shared.add(key)
    remaining = list(shared)
    while remaining:
        key = remaining.pop()
        for used in declarations[key].uses & index.keys():
            target = index[used]
            if target in shared:
                continue
            if declarations[target].test or used in ASSERTIONS:
                raise ValueError(f"shared fixture depends on test or assertion: {used}")
            shared.add(target)
            remaining.append(target)

    export = {
        name: name[0].upper() + name[1:]
        for key in shared for name in declarations[key].names
    }
    fixtures = defaultdict(list)
    test_groups = defaultdict(list)
    extra_aliases = defaultdict(dict)
    fixtures_path = f'"{MODULE}{FIXTURES}"'
    compiler_path = f'compiler "{MODULE}{ROOT}"'
    original_imports = {path: import_lines(path.read_text()) for path in files}
    for key, decl in enumerate(declarations):
        if key in shared:
            body = rename(decl.body, export)
            name = decl.names[0]
            if name in OUTPUT_COMPILERS:
                body = body.replace(f"func {export[name]}(", f"func (compile OutputCompiler) {export[name]}(", 1)
                body = rename(body, {"CompileRecipeOutputWithPolicy": "compile"})
                body = rename(body, {export[item]: "compile." + export[item]
                                     for item in OUTPUT_COMPILERS if item != name})
            elif "CompileRecipeOutputWithPolicy" in decl.uses:
                raise ValueError(f"unexpected root compiler dependency: {name}")
            fixtures[decl.source].append(body)
            continue
        for owner in sorted(owners[key]):
            body = decl.body
            if owner == "render/aql@internal":
                body = body.replace("aql.RenderCategoryScan", "RenderCategoryScan")
            test_groups[owner, decl.source].append(body)
            for name in sorted(decl.uses & export.keys()):
                fixture = declarations[index[name]]
                target = f"compilerfixtures.{export[name]}"
                if name in OUTPUT_COMPILERS:
                    compile = "CompileRecipeOutputWithPolicy" if owner == "root" else "compiler.CompileRecipeOutputWithPolicy"
                    target = f"compilerfixtures.OutputCompiler({compile}).{export[name]}"
                category = fixture.body.lstrip().split()[0]
                if category not in ("type", "const"):
                    category = "var"
                extra_aliases[owner][name] = f"{category} {name} = {target}"
            if owner == "root":
                continue
            for name in sorted(decl.uses & source.keys()):
                if name == "categoryLimitBind" and owner == "render/aql@internal":
                    continue
                if name[0].islower():
                    raise ValueError(f"private compiler reference: {name}")
                category = source[name].lstrip().split()[0]
                if category not in ("type", "const"):
                    category = "var"
                extra_aliases[owner][name] = f"{category} {name} = compiler.{name}"

    touched = []
    FIXTURES.mkdir(exist_ok=True, parents=True)
    if OUTPUT_COMPILERS & export.keys():
        path = FIXTURES / "outputcompiler.go"
        path.write_text(file_text("compilerfixtures", [
            f'"{MODULE}{ROOT}/ir"', f'"{MODULE}{ROOT}/lower"', f'"{MODULE}{ROOT}/model"',
            f'"{MODULE}{ROOT.parent}/recipe"'
        ], ["type OutputCompiler func(lower.CompiledRecipeOutput, recipe.RuntimeBindings, int, ir.PhysicalOptimizationPolicy) (model.CompiledQuery, error)"]))
        touched.append(path)
    for path, bodies in sorted(fixtures.items()):
        destination = FIXTURES / (source_stem(path) + ".go")
        destination.write_text(file_text("compilerfixtures", original_imports[path], bodies))
        touched.append(destination)
    for path in files:
        if ("root", path) not in test_groups:
            path.unlink()
    for (owner, path), bodies in sorted(test_groups.items()):
        if owner == "root":
            if len(bodies) == len(raw[str(path)]):
                continue
            destination = path
            package = "compiler"
        else:
            destination = output_directory(owner) / path.name
            if destination.exists():
                destination = destination.with_name("compiler" + destination.name)
            if destination.exists():
                raise ValueError(f"destination exists: {destination}")
            package = package_name(owner)
        destination.write_text(file_text(package, original_imports[path], bodies))
        touched.append(destination)
    for owner, aliases in sorted(extra_aliases.items()):
        directory = ROOT if owner == "root" else output_directory(owner)
        package = "compiler" if owner == "root" else package_name(owner)
        name = "compilerfixturebindings_test.go" if owner.endswith("@internal") else "compilerbindings_test.go"
        path = directory / name
        path.write_text(file_text(package, [fixtures_path, compiler_path], aliases.values()))
        touched.append(path)
    subprocess.run(["goimports", "-w", *map(str, touched)], check=True)

def javascript_strings(names):
    return "[" + ", ".join(f"'{name}'" for name in names) + "]"


def update_runner():
    runner = Path("scripts/run-arango-integration.mjs")
    source = runner.read_text()
    block = "profiles.find(p => p.id === 'compiler').alsoRequired = ["
    start = source.index(block)
    end = source.index("];", start)
    names = re.findall(r"'(Test\w+)'", source[start:end])
    tests = {}
    for path in ROOT.rglob("*_test.go"):
        for name in re.findall(r"^func (Test\w+)\(", path.read_text(), re.M):
            if name in names:
                if name in tests:
                    raise ValueError(f"ambiguous required test: {name}")
                tests[name] = path.parent
    non_root = defaultdict(list)
    for name in names:
        package = tests[name]
        if package != ROOT:
            non_root[package].append(name)
            line = f"    '{name}',\n"
            if source.count(line) != 1:
                raise ValueError(f"ambiguous runner entry: {name}")
            source = source.replace(line, "", 1)
    for name in (
        "TestPopulationMappingRepeatedRouteDeduplicatesScopedFrontiersAgainstArango",
        "TestPopulationMappingRoutedCohortPreservesSelectedMembersAgainstArango",
    ):
        package = ROOT / "population"
        if not any(f"func {name}(" in path.read_text() for path in package.glob("*_test.go")):
            raise ValueError(f"lost population integration case: {name}")
        non_root[package].append(name)
    profiles = "profiles.find(p => p.id === 'compiler').alsoPackages = [\n"
    additions = []
    for package, required in sorted(non_root.items()):
        if package == ROOT / "render/aql":
            original = "required: 'TestScalarSelectorArrayMatchesLegacyAgainstArango'"
            extra = javascript_strings(required)
            source = source.replace(original, f"{original}, alsoRequired: {extra}", 1)
        else:
            optional = f", alsoRequired: {javascript_strings(required[1:])}" if required[1:] else ""
            additions.append(f"    {{ package: './{package}', required: '{required[0]}'{optional} }},\n")
    source = source.replace(profiles, profiles + "".join(additions), 1)
    source = source.replace("required: extra.required, alsoRequired: []",
                            "required: extra.required, alsoRequired: extra.alsoRequired || []", 1)
    runner.write_text(source)



if __name__ == "__main__":
    place_tests()
    update_runner()
