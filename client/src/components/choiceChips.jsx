// Fork addition: one decision, one control. A handful of named options shown
// side by side, of which exactly one is picked: for settings that used to be
// two inputs that had to agree (a port and "uses TLS"). The picked option is
// described in a sentence below. Same look as the schedule presets of
// cronInput.jsx; to the keyboard and to a screen reader it is a radio group.
import * as React from 'react';
import { Chip, FormHelperText, InputLabel, Stack } from '@mui/material';
import { FormReadOnlyContext } from '../pages/config/users/formShortcuts';

// options: [{ value, label, description }]
const ChoiceChips = ({ id, label, value, options, onChange, helperText, error, disabled }) => {
  const readOnly = React.useContext(FormReadOnlyContext);
  const off = disabled || readOnly;
  const picked = options.find((option) => option.value === value);
  const move = (event, index) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
    if (!step) return;
    event.preventDefault();
    const next = options[(index + step + options.length) % options.length];
    onChange(next.value);
    const chips = event.currentTarget.parentElement.querySelectorAll('[role=radio]');
    if (chips[(index + step + options.length) % options.length]) chips[(index + step + options.length) % options.length].focus();
  };
  return <Stack spacing={1}>
    {label && <InputLabel id={id + '-label'}>{label}</InputLabel>}
    <Stack id={id} direction="row" useFlexGap flexWrap="wrap" spacing={1} role="radiogroup" aria-labelledby={label ? id + '-label' : undefined}>
      {options.map((option, index) => {
        const on = option.value === value;
        return <Chip
          key={option.value}
          role="radio"
          aria-checked={on}
          tabIndex={on || (!picked && index === 0) ? 0 : -1}
          clickable
          disabled={off}
          color={on ? 'primary' : 'default'}
          variant={on ? 'filled' : 'outlined'}
          label={option.label}
          onClick={() => onChange(option.value)}
          onKeyDown={(event) => move(event, index)}
          // a long label wraps inside its chip instead of widening the page
          sx={{ maxWidth: '100%', height: 'auto', minHeight: 32, '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5 } }}
        />;
      })}
    </Stack>
    {error
      ? <FormHelperText error>{error}</FormHelperText>
      : ((picked && picked.description) || helperText) && <FormHelperText>{(picked && picked.description) || helperText}</FormHelperText>}
  </Stack>;
};

export default ChoiceChips;
