import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { compileScript, parse } from 'vue/compiler-sfc'
import { createRenderer, type Component } from 'vue'
import ts from 'typescript'

// Mounts components without a DOM: unit tests exercise setup logic only.
export function createTestRenderer<Node extends object = object>(
  node: () => Node = () => ({}) as Node,
) {
  return createRenderer<Node, Node>({
    createElement: node,

    createText: node,

    createComment: node,

    setText() {},

    setElementText() {},

    insert() {},

    remove() {},

    patchProp() {},

    parentNode: () => null,

    nextSibling: () => null,
  })
}

// Runs a component's real script. Child components are stubs and `mocks` replace
// modules by their import specifier; everything else is the real module.
export function loadSetup(path: string, mocks: Record<string, unknown> = {}): Component {
  const file = new URL('../src/' + path, import.meta.url)
  const { descriptor } = parse(readFileSync(file, 'utf8'))
  const js = ts.transpileModule(compileScript(descriptor, { id: 'unit' }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const require = createRequire(file)
  const module = { exports: {} as { default: Record<string, unknown> } }
  new Function('require', 'module', 'exports', js)(
    (name: string) => mocks[name] ?? (name.endsWith('.vue') ? { default: {} } : require(name)),
    module,
    module.exports,
  )
  return { ...module.exports.default, render: () => null }
}
