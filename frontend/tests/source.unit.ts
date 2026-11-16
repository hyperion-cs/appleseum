import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import ts from 'typescript'
import { parse } from 'vue/compiler-sfc'

const root = fileURLToPath(new URL('../src/', import.meta.url))

test('TypeScript and Vue script decimal literals use digit separators', () => {
  const failures: string[] = []

  function checkScript(path: string, text: string, lineOffset = 0) {
    const source = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)

    function visit(node: ts.Node) {
      if (ts.isNumericLiteral(node)) {
        const literal = node.getText(source)
        if (/^[1-9][0-9_]*$/.test(literal) && !/^[1-9][0-9]{0,2}(?:_[0-9]{3})*$/.test(literal)) {
          const { line } = source.getLineAndCharacterOfPosition(node.getStart(source))
          failures.push(`${path}:${line + lineOffset + 1}: ${literal}`)
        }
      }
      ts.forEachChild(node, visit)
    }

    visit(source)
  }

  function check(directory: string) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) {
        check(path)
        continue
      }
      if (!/\.(ts|vue)$/.test(entry.name)) continue
      const text = readFileSync(path, 'utf8')
      if (entry.name.endsWith('.ts')) checkScript(path, text)
      else {
        const { descriptor } = parse(text, { filename: path })
        for (const block of [descriptor.script, descriptor.scriptSetup]) {
          if (block) checkScript(path, block.content, block.loc.start.line - 1)
        }
      }
    }
  }

  check(root)
  assert.deepEqual(failures, [])
})

// Lower layers first; a module may import its own layer and the ones before it.
const layers = [['platform', 'api'], ['i18n'], ['composables'], ['components'], ['views'], ['app']]

test('modules depend only on their own and lower layers', () => {
  const failures: string[] = []

  const rank = (path: string) =>
    layers.findIndex((names) => names.includes(path.split(/[/\\]/)[0]!))

  function checkScript(path: string, text: string) {
    const source = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)

    function visit(node: ts.Node) {
      let dependency: string | undefined
      if (
        (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
        node.moduleSpecifier &&
        ts.isStringLiteral(node.moduleSpecifier)
      )
        dependency = node.moduleSpecifier.text
      if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
        const argument = node.arguments[0]
        if (argument && ts.isStringLiteral(argument)) dependency = argument.text
      }
      if (dependency?.startsWith('.')) {
        const target = relative(root, resolve(dirname(path), dependency))
        if (rank(target) > rank(relative(root, path)))
          failures.push(`${relative(root, path)} imports ${target}`)
      }
      ts.forEachChild(node, visit)
    }

    visit(source)
  }

  function check(directory: string) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) check(path)
      else if (entry.name.endsWith('.ts')) checkScript(path, readFileSync(path, 'utf8'))
      else if (entry.name.endsWith('.vue')) {
        const { descriptor } = parse(readFileSync(path, 'utf8'), { filename: path })
        for (const block of [descriptor.script, descriptor.scriptSetup])
          if (block) checkScript(path, block.content)
      }
    }
  }

  for (const layer of layers.flat()) check(join(root, layer))
  assert.deepEqual(failures, [])
})
