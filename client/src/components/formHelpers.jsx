// Fork addition: small pieces the reworked forms share, next to the ones the
// email form brought (choiceChips.jsx, proposalBox.jsx, formField.jsx,
// utils/proposals.js; the rules are in the form principles the fork follows).
//   ChoiceRow      choiceChips.jsx as a row of a form's grid, like the
//                  Cosmos* helpers of pages/config/users/formShortcuts.jsx,
//                  with a mark for a mandatory choice and room below the
//                  chips for what belongs to the choice
//   ProposalNote   the line under a field that says where its value came from
//   keepOnBackdrop a dialog that holds a form does not close on a click
//                  beside it
import * as React from 'react';
import { FormHelperText, Grid, Stack } from '@mui/material';
import ChoiceChips from './choiceChips';

// options: [{ value, label, hint }]: the hint of the picked option is the
// sentence under the chips. `note` is about the whole choice and is shown as
// well (why it cannot be changed, for instance).
export const ChoiceRow = ({ name, label, required, options, value, onChange, error, disabled, note, children }) => (
  <Grid item xs={12}>
    <Stack spacing={1}>
      <ChoiceChips
        id={name}
        // the mark of a mandatory field, as MUI's own labels carry it
        label={label && required ? <>{label}<span aria-hidden="true" className="MuiInputLabel-asterisk">&thinsp;*</span></> : label}
        value={value}
        options={options.map((option) => ({ value: option.value, label: option.label, description: option.hint }))}
        onChange={onChange}
        error={error}
        disabled={disabled}
      />
      {children}
      {note && <FormHelperText>{note}</FormHelperText>}
    </Stack>
  </Grid>
);

export const ProposalNote = ({ children }) => (children
  ? <FormHelperText sx={{ overflowWrap: 'anywhere' }}>{children}</FormHelperText>
  : null);

// A dialog that holds a form does not close on a click beside it: what was
// typed would be lost by accident. Escape and its own buttons still close it.
//   <Dialog onClose={keepOnBackdrop(() => setOpen(false))}>
export const keepOnBackdrop = (close) => (event, reason) => {
  if (reason !== 'backdropClick') {
    close(event, reason);
  }
};
