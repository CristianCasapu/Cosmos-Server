// Fork addition: the templates of the compose installer (Market apps and
// compose imports: {ServiceName}, {Passwords.0}, {if x}...{else}...{/if},
// {for x in list}...{/for}, {>partial}, {!comment!}) without turning them into
// code. The library the installer used (whiskers 0.4) compiles every template
// with `new Function`, which the panel's script policy forbids (no
// 'unsafe-eval', see src/fork_csp.go). This module reads the template the same
// way whiskers does, step for step, and walks the result instead of running
// it; whiskersSafe.test.mjs checks it against the library on fixed and on
// random templates.
//
// What whiskers does, and so what this does:
//   - "\r" is dropped; a tag or comment preceded by backslashes is printed as
//     text, with one backslash less;
//   - {key.path} prints the value (missing, null, false, [] and {} print
//     nothing; 0 prints 0); {>key} renders the value as a template with the
//     same context;
//   - {for item in list} sets context[item] to each element of the list in
//     turn and puts back what it held before at {/for}; {else} inside a loop
//     runs when the list is empty; {if key} / {if not key}, with {else};
//   - a close tag that does not match is dropped, blocks left open close at
//     the end of the template (an open loop then keeps its last element);
//   - a loop variable that would not be a JavaScript name is an error, as it
//     is a syntax error in whiskers.

const TAG = /(\\*){(?:([\w_.\-@]+)|>([\w_.\-@]+)|for +([\w_\-@]+) +in +([\w_.\-@]+)|if +(not +|)([\w_.\-@]+)|\/(for|if))}/g;
const COMMENT = /(\\*){![\s\S]*?!}/g;
const NAME = /^[A-Za-z_$][\w$]*$/;

// whiskers' own lookup: dot notation, "" for what it treats as empty
const get = (obj, key) => {
  const accessor = key.split('.');
  for (let i = 0; i < accessor.length; i++) {
    if (!obj) return '';
    obj = obj[accessor[i]];
  }
  if (obj === undefined || obj === null || obj === false) return '';
  if (obj instanceof Array && obj.length === 0) return '';
  if (obj.constructor === Object) {
    let empty = true;
    for (const i in obj) if (Object.prototype.hasOwnProperty.call(obj, i)) empty = !i;
    if (empty) return '';
  }
  return obj;
};

// The text between tags is kept as whiskers keeps it, escaped for a string
// literal (\\, \' and \n): read it back the way the literal would be read.
const literal = (text) => text.replace(/\\([\s\S])/g, (all, c) => (c === 'n' ? '\n' : c));

const parse = (template) => {
  let source = (template || '') + '';
  source = source.replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\n/g, '\\n').replace(/\r/g, '');
  source = source.replace(COMMENT, (str, escapeChar) => (escapeChar ? str.replace('\\\\', '') : ''));

  const root = { body: [] };
  const stack = [];
  // where text and tags go now: the body of the innermost open block, or its else
  const target = () => {
    const block = stack[stack.length - 1];
    if (!block) return root.body;
    return block.elsed ? block.otherwise : block.body;
  };
  let text = '';
  const flush = () => {
    if (text) target().push({ text: literal(text) });
    text = '';
  };

  let last = 0;
  source.replace(TAG, (str, escapeChar, key, partial, iterVar, forKey, ifNot, ifKey, closeStatement, offset) => {
    text += source.slice(last, offset);
    last = offset + str.length;
    if (escapeChar) {
      text += str.replace('\\\\', '');
      return '';
    }
    if (key) {
      if (key === 'else') {
        const block = stack[stack.length - 1];
        if (block && !block.elsed) {
          flush();
          block.elsed = true;
          return '';
        }
        console.warn('extra {else} ignored');
        return '';
      }
      flush();
      target().push({ key });
      return '';
    }
    if (partial) {
      flush();
      target().push({ partial });
      return '';
    }
    if (forKey) {
      flush();
      const safe = iterVar.replace('-', '__');
      if (!NAME.test(safe + 'A')) throw new SyntaxError('Invalid loop variable in template: ' + iterVar);
      const block = { statement: 'for', iterVar, safe, forKey, body: [], otherwise: [], elsed: false, closed: false };
      target().push(block);
      stack.push(block);
      return '';
    }
    if (ifKey) {
      flush();
      const block = { statement: 'if', cond: ifKey, not: !!ifNot, body: [], otherwise: [], elsed: false };
      target().push(block);
      stack.push(block);
      return '';
    }
    if (closeStatement) {
      const block = stack[stack.length - 1];
      if (block && block.statement === closeStatement) {
        flush();
        block.closed = true;
        stack.pop();
        return '';
      }
      console.warn('extra {/' + closeStatement + '} ignored');
      return '';
    }
    text += str;
    return '';
  });
  text += source.slice(last);
  flush();
  for (let i = stack.length - 1; i > -1; i--) {
    console.warn('extra {' + stack[i].statement + '} closed at end of template');
  }
  return root.body;
};

// out.b is the text so far; out.vars the loop's state per loop variable, shared
// by every loop on the same variable as whiskers' generated `var`s are
const run = (nodes, c, out) => {
  for (const node of nodes) {
    if (node.text !== undefined) out.b += node.text;
    else if (node.key !== undefined) out.b += get(c, node.key);
    else if (node.partial !== undefined) out.b += render(get(c, node.partial), c);
    else if (node.statement === 'if') {
      if (node.not ? !get(c, node.cond) : get(c, node.cond)) run(node.body, c, out);
      else run(node.otherwise, c, out);
    } else if (node.statement === 'for') {
      const v = out.vars[node.safe] || (out.vars[node.safe] = {});
      v.saved = get(c, node.iterVar);
      v.list = get(c, node.forKey);
      for (v.i = 0; v.i < v.list.length; v.i++) {
        c[node.iterVar] = v.list[v.i];
        run(node.body, c, out);
      }
      if (node.elsed && !get(c, node.forKey)) run(node.otherwise, c, out);
      // whiskers puts the old value back at {/for}: a loop left open keeps the last element
      if (node.closed) c[node.iterVar] = v.saved;
    }
  }
};

export const compile = (template) => {
  if (template instanceof Function) return template;
  const nodes = parse(template);
  return (context) => {
    const out = { b: '', vars: Object.create(null) };
    run(nodes, context, out);
    return out.b;
  };
};

// (no cache of compiled templates as whiskers keeps: the installer renders a
// new template at each keystroke, and reading one again is cheap)
export const render = (template, context) => compile(template)(context);

export default { render, compile };
