// Fork addition: shared pieces of the forms that propose instead of asking.
//   ChoiceChips    one decision whose valid answers are known: chips to pick
//                  from in place of a free text field or a row of switches,
//                  with a line saying what the picked answer means
//   ProposalNote   the line under a field that says where a proposed value
//                  came from
// They sit in a form's grid like the Cosmos* helpers of
// pages/config/users/formShortcuts.jsx and use the same parts.
import * as React from 'react';
import { Chip, FormHelperText, Grid, InputLabel, Stack } from '@mui/material';
import { FormReadOnlyContext } from '../pages/config/users/formShortcuts';

// options: [{ value, label, hint, icon }]. The `hint` of the picked option is
// shown below the chips, then `helperText`, which is about the whole choice.
export const ChoiceChips = ({ name, label, required, options, value, onChange, helperText, error, disabled, children }) => {
  disabled = disabled || React.useContext(FormReadOnlyContext);
  const picked = options.find((option) => option.value === value);
  const hint = picked && picked.hint;

  return <Grid item xs={12}>
    <Stack spacing={1}>
      {label && <InputLabel id={name + '-label'} required={required}>{label}</InputLabel>}
      <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1} role="radiogroup" aria-labelledby={label ? name + '-label' : undefined} id={name}>
        {options.map((option) => {
          const selected = option.value === value;
          return <Chip
            key={String(option.value)}
            role="radio"
            aria-checked={selected}
            clickable={!disabled}
            disabled={disabled}
            icon={option.icon}
            color={selected ? 'primary' : 'default'}
            variant={selected ? 'filled' : 'outlined'}
            label={option.label}
            onClick={disabled ? undefined : () => onChange(option.value)}
            sx={{ maxWidth: '100%' }}
          />;
        })}
      </Stack>
      {children}
      {error && <FormHelperText error>{error}</FormHelperText>}
      {!error && hint && <FormHelperText>{hint}</FormHelperText>}
      {helperText && <FormHelperText>{helperText}</FormHelperText>}
    </Stack>
  </Grid>;
};

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
