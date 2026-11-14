// Fork addition: values a form proposes, and the rule every form of the fork
// follows for them: a proposal fills a field that is empty or that still
// holds an earlier proposal, and never replaces what the administrator typed
// (or what was loaded from the saved settings).
//
// The form keeps `marks`: for each field a proposal filled, the value it put
// there. A field whose value still equals its mark has not been touched
// since, so the next proposal may replace it. Nothing has to listen to key
// presses: typing makes the value differ from the mark, and that is all.
//
// Fields that only make sense together (a server and its port) are a
// `unit`: the unit takes a proposal whole or not at all.

export const isEmptyValue = (value) => value === undefined || value === null
  || (typeof value === 'string' && value.trim() === '');

const same = (a, b) => (typeof a === 'boolean' || typeof b === 'boolean' ? a === b : String(a) === String(b));

// stillProposed: the field holds what a proposal put there.
export const stillProposed = (values, marks, name) => !!marks
  && Object.prototype.hasOwnProperty.call(marks, name) && same(values[name], marks[name]);

// A unit may be filled when none of its fields holds a value of the
// administrator's own. A switch (true/false) is never "empty" by itself: it
// follows the other fields of its unit.
const unitIsFree = (values, marks, unit) => {
  const texts = unit.filter((name) => typeof values[name] !== 'boolean');
  if (texts.length === 0) {
    return unit.every((name) => stillProposed(values, marks, name));
  }
  const textsFree = texts.every((name) => isEmptyValue(values[name]) || stillProposed(values, marks, name));
  const textsEmpty = texts.every((name) => isEmptyValue(values[name]));
  const switchesFree = unit
    .filter((name) => typeof values[name] === 'boolean')
    .every((name) => stillProposed(values, marks, name) || textsEmpty);
  return textsFree && switchesFree;
};

// applyProposal returns:
//   values: the fields to set in the form (only those that change)
//   marks:  the new marks
//   held:   the proposed fields that were left alone because the form holds
//           another value there; the form shows the proposal beside them
// `proposal` holds only the fields it has something for. `units` lists the
// fields that go together; a field in no unit stands alone.
export const applyProposal = (values, proposal, marks = {}, units = []) => {
  const names = Object.keys(proposal).filter((name) => proposal[name] !== undefined);
  const grouped = units
    .map((unit) => unit.filter((name) => names.includes(name)))
    .filter((unit) => unit.length > 0);
  const inUnit = grouped.flat();
  const all = [...grouped, ...names.filter((name) => !inUnit.includes(name)).map((name) => [name])];

  const set = {};
  const nextMarks = { ...marks };
  const held = [];
  all.forEach((unit) => {
    if (unitIsFree(values, marks, unit)) {
      unit.forEach((name) => {
        if (!same(values[name], proposal[name])) set[name] = proposal[name];
        nextMarks[name] = proposal[name];
      });
    } else {
      unit.forEach((name) => {
        if (!same(values[name], proposal[name])) held.push(name);
        // a field of the unit that happens to hold the proposed value is not marked: the unit is the admin's
        delete nextMarks[name];
      });
    }
  });
  return { values: set, marks: nextMarks, held };
};

// acceptProposal is the administrator saying "use these": every proposed
// field is set, and marked as a proposal again.
export const acceptProposal = (values, proposal) => {
  const set = {};
  const marks = {};
  Object.keys(proposal).filter((name) => proposal[name] !== undefined).forEach((name) => {
    if (!same(values[name], proposal[name])) set[name] = proposal[name];
    marks[name] = proposal[name];
  });
  return { values: set, marks };
};
