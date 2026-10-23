#!/usr/bin/env python3
"""静态自检：tools/browser/*.py 内对同模块函数的调用参数必须与定义匹配。

背景：9e1bb2e 删掉 goto_admin_top_level 的 guard 形参后漏改调用点，
`py_compile`、`pyflakes` 都查不出，smoke 一启动就 TypeError。
本测试只用 ast 解析，不导入 playwright，CI 无需安装浏览器依赖：

    python3 -m unittest discover -s tools -p 'test_*.py' -v
"""

from __future__ import annotations

import ast
import unittest
from dataclasses import dataclass, field
from pathlib import Path

TOOLS_DIR = Path(__file__).resolve().parent
BROWSER_DIR = TOOLS_DIR / "browser"


@dataclass(frozen=True)
class _Function:
    name: str
    lineno: int
    pos: tuple[str, ...]
    posonly: tuple[str, ...]
    required_pos: int
    kwonly: tuple[str, ...]
    required_kwonly: tuple[str, ...]
    vararg: bool
    kwarg: bool


@dataclass
class _Scope:
    bindings: dict[str, set[str]] = field(default_factory=dict)
    globals: set[str] = field(default_factory=set)

    @property
    def local(self) -> set[str]:
        return set(self.bindings) - self.globals


def _signature(node: ast.FunctionDef | ast.AsyncFunctionDef) -> _Function:
    args = node.args
    posonly = tuple(a.arg for a in args.posonlyargs)
    pos = posonly + tuple(a.arg for a in args.args)
    return _Function(
        name=node.name,
        lineno=node.lineno,
        pos=pos,
        posonly=posonly,
        required_pos=len(pos) - len(args.defaults),
        kwonly=tuple(a.arg for a in args.kwonlyargs),
        required_kwonly=tuple(
            a.arg for a, default in zip(args.kwonlyargs, args.kw_defaults) if default is None
        ),
        vararg=args.vararg is not None,
        kwarg=args.kwarg is not None,
    )


class _Collector(ast.NodeVisitor):
    """收集一个作用域直接绑定的名字；不进入嵌套函数/类体。"""

    def __init__(self) -> None:
        self.scope = _Scope()
        self.functions: dict[str, _Function] = {}

    def _bind(self, name: str, kind: str) -> None:
        self.scope.bindings.setdefault(name, set()).add(kind)

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        self._bind(node.name, "def")
        self.functions[node.name] = _signature(node)

    visit_AsyncFunctionDef = visit_FunctionDef  # type: ignore[assignment]

    def visit_Lambda(self, node: ast.Lambda) -> None:
        return

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        self._bind(node.name, "class")

    def visit_Name(self, node: ast.Name) -> None:
        if isinstance(node.ctx, (ast.Store, ast.Del)):
            self._bind(node.id, "assign")

    def visit_arg(self, node: ast.arg) -> None:
        self._bind(node.arg, "arg")

    def visit_Import(self, node: ast.Import) -> None:
        for alias in node.names:
            self._bind((alias.asname or alias.name).split(".")[0], "import")

    def visit_ImportFrom(self, node: ast.ImportFrom) -> None:
        for alias in node.names:
            if alias.name != "*":
                self._bind(alias.asname or alias.name, "import")

    def visit_ExceptHandler(self, node: ast.ExceptHandler) -> None:
        if node.name:
            self._bind(node.name, "assign")
        self.generic_visit(node)

    def visit_Global(self, node: ast.Global) -> None:
        self.scope.globals.update(node.names)


def module_functions(source: str, filename: str = "<string>") -> dict[str, _Function]:
    """模块级、且只被这一处 def 绑定的函数（重名/被赋值覆盖的不检查）。"""
    collector = _Collector()
    collector.visit(ast.parse(source, filename=filename))
    return {
        name: fn
        for name, fn in collector.functions.items()
        if collector.scope.bindings.get(name) == {"def"}
    }


def _function_scope(node: ast.FunctionDef | ast.AsyncFunctionDef) -> _Scope:
    collector = _Collector()
    for stmt in node.body:
        collector.visit(stmt)
    scope = collector.scope
    for arg in (*node.args.posonlyargs, *node.args.args, *node.args.kwonlyargs):
        scope.bindings.setdefault(arg.arg, set()).add("arg")
    for extra in (node.args.vararg, node.args.kwarg):
        if extra is not None:
            scope.bindings.setdefault(extra.arg, set()).add("arg")
    return scope


def _check_call(
    call: ast.Call, fn: _Function, filename: str, problems: list[str]
) -> None:
    where = f"{filename}:{call.lineno}"
    definition = f"定义见第 {fn.lineno} 行"
    npos = sum(1 for a in call.args if not isinstance(a, ast.Starred))
    starred = npos != len(call.args)
    keywords = {k.arg for k in call.keywords if k.arg is not None}
    dstar = any(k.arg is None for k in call.keywords)

    if not starred and not fn.vararg and npos > len(fn.pos):
        problems.append(
            f"{where}: 调用 {fn.name}() 传了 {npos} 个位置参数，"
            f"但只接受 {len(fn.pos)} 个（{definition}）"
        )
    if not fn.kwarg:
        unknown = sorted(keywords - set(fn.kwonly) - set(fn.pos))
        if unknown:
            problems.append(
                f"{where}: 调用 {fn.name}() 用了未知关键字参数 {unknown}（{definition}）"
            )
    positional_only = sorted(keywords & set(fn.posonly))
    if positional_only:
        problems.append(
            f"{where}: 调用 {fn.name}() 用关键字传了仅位置参数 {positional_only}（{definition}）"
        )
    for name in sorted(keywords & set(fn.pos[:npos])):
        problems.append(f"{where}: 调用 {fn.name}() 重复传入 {name!r}（{definition}）")
    if starred or dstar:
        return
    provided = set(fn.pos[:npos]) | keywords
    missing = [name for name in fn.pos[: fn.required_pos] if name not in provided]
    missing += [name for name in fn.required_kwonly if name not in provided]
    if missing:
        problems.append(
            f"{where}: 调用 {fn.name}() 缺少必填参数 {missing}（{definition}）"
        )


def _walk_body(
    body: list[ast.stmt],
    scopes: list[_Scope],
    functions: dict[str, _Function],
    filename: str,
    problems: list[str],
) -> None:
    for stmt in body:
        _walk(stmt, scopes, functions, filename, problems)


def _walk(
    node: ast.AST,
    scopes: list[_Scope],
    functions: dict[str, _Function],
    filename: str,
    problems: list[str],
) -> None:
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
        fn = functions.get(node.func.id)
        if fn is not None and not any(node.func.id in scope.local for scope in scopes):
            _check_call(node, fn, filename, problems)
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        _walk_body(node.body, [*scopes, _function_scope(node)], functions, filename, problems)
        return
    if isinstance(node, ast.Lambda):
        return
    if isinstance(node, ast.ClassDef):
        _walk_body(node.body, scopes, functions, filename, problems)
        return
    for child in ast.iter_child_nodes(node):
        _walk(child, scopes, functions, filename, problems)


def check_source(source: str, filename: str = "<string>") -> list[str]:
    """返回该源码里所有「同模块函数调用的参数与定义不匹配」问题描述。"""
    problems: list[str] = []
    _walk_body(ast.parse(source, filename=filename).body, [], module_functions(source, filename), filename, problems)
    return problems


class BrowserCallArityTest(unittest.TestCase):
    def test_browser_scripts_calls_match_definitions(self):
        files = sorted(BROWSER_DIR.glob("*.py"))
        self.assertTrue(files, f"{BROWSER_DIR} 下没有 Python 脚本")
        problems: list[str] = []
        for path in files:
            rel = path.relative_to(TOOLS_DIR.parent)
            problems.extend(check_source(path.read_text(encoding="utf-8"), str(rel)))
        self.assertEqual(problems, [], "函数调用参数与定义不匹配：\n" + "\n".join(problems))

    def test_scanner_sees_smoke_entry_functions(self):
        """防空跑：确认收集器真的读到了 smoke 的模块级函数。"""
        source = (BROWSER_DIR / "admin_embed_entry.py").read_text(encoding="utf-8")
        functions = module_functions(source, "admin_embed_entry.py")
        self.assertIn("goto_admin_top_level", functions)
        self.assertGreaterEqual(len(functions), 10)

    def test_detects_too_many_positional(self):
        source = (
            "def goto_admin_top_level(page, base, shot_dir, tag):\n"
            "    pass\n\n"
            "def run_viewport(page):\n"
            "    goto_admin_top_level(page, 'x', None, 'tag', None)\n"
        )
        problems = check_source(source, "snippet.py")
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("5 个位置参数", problems[0])
        self.assertIn("goto_admin_top_level", problems[0])

    def test_detects_missing_required_and_unknown_keyword(self):
        source = (
            "def f(a, b, *, c):\n"
            "    pass\n\n"
            "def g():\n"
            "    f(1, c=2, d=3)\n"
        )
        problems = check_source(source, "snippet.py")
        self.assertEqual(len(problems), 2, problems)
        self.assertTrue(any("缺少必填参数" in p and "'b'" in p for p in problems), problems)
        self.assertTrue(any("未知关键字参数" in p and "'d'" in p for p in problems), problems)

    def test_accepts_defaults_varargs_kwonly(self):
        source = (
            "def f(a, b=1, *args, c=2, **kwargs):\n"
            "    pass\n\n"
            "def g():\n"
            "    f(1)\n"
            "    f(1, 2, 3, 4, c=5, d=6)\n"
        )
        self.assertEqual(check_source(source, "snippet.py"), [])

    def test_ignores_methods_builtins_and_shadowed_names(self):
        source = (
            "def target(a):\n"
            "    pass\n\n"
            "def g(target):\n"
            "    target(1, 2, 3)\n"
            "    print(1, 2, 3)\n"
            "    obj.target(1, 2, 3)\n"
        )
        self.assertEqual(check_source(source, "snippet.py"), [])


if __name__ == "__main__":
    unittest.main()
