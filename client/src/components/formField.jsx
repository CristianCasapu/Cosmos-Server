// Fork addition: a labelled field that looks like the form shortcuts
// (pages/config/users/formShortcuts.jsx) but holds no Formik field of its
// own: its value and what a change does are given by the form. For fields
// that show a part of a stored value (the sender's name and address are one
// setting), and for a list to pick from that acts instead of storing.
//
// Under the field, in this order of precedence: the error, else the help
// text. A help text says what to enter in plain words and gives an example.
import * as React from 'react';
import { FormControl, FormHelperText, InputLabel, MenuItem, OutlinedInput, Select, Stack } from '@mui/material';
import { FormReadOnlyContext } from '../pages/config/users/formShortcuts';

export const FormField = ({
  id, label, value, onChange, onBlur, onEnter, helperText, error, required, type, placeholder,
  inputProps, inputMode, endAdornment, autoComplete, disabled, children,
}) => {
  const readOnly = React.useContext(FormReadOnlyContext);
  return <Stack spacing={1}>
    {label && <InputLabel htmlFor={id} required={required}>{label}</InputLabel>}
    <OutlinedInput
      id={id}
      name={id}
      type={type || 'text'}
      value={value === undefined || value === null ? '' : value}
      disabled={disabled || readOnly}
      placeholder={placeholder}
      autoComplete={autoComplete}
      fullWidth
      error={Boolean(error)}
      endAdornment={endAdornment}
      inputProps={{ ...(required ? { 'aria-required': true } : {}), ...(inputMode ? { inputMode } : {}), ...inputProps }}
      onChange={(event) => onChange(event.target.value)}
      onBlur={onBlur}
      onKeyDown={(event) => {
        // inside a settings form Enter would save the whole page
        if (onEnter && event.key === 'Enter') {
          event.preventDefault();
          onEnter();
        }
      }}
    />
    {error
      ? <FormHelperText error>{error}</FormHelperText>
      : (helperText && <FormHelperText>{helperText}</FormHelperText>)}
    {children}
  </Stack>;
};

// options: [[value, label], ...]
export const FormSelect = ({ id, label, value, onChange, options, helperText, disabled, emptyLabel }) => {
  const readOnly = React.useContext(FormReadOnlyContext);
  return <Stack spacing={1}>
    {label && <InputLabel htmlFor={id}>{label}</InputLabel>}
    <FormControl variant="outlined" fullWidth disabled={disabled || readOnly}>
      <Select
        variant="outlined"
        id={id}
        name={id}
        value={value || ''}
        displayEmpty
        onChange={(event) => onChange(event.target.value)}
      >
        {emptyLabel !== undefined && <MenuItem value=""><em>{emptyLabel}</em></MenuItem>}
        {options.map((option) => <MenuItem key={option[0]} value={option[0]}>{option[1]}</MenuItem>)}
      </Select>
      {helperText && <FormHelperText sx={{ mx: 0 }}>{helperText}</FormHelperText>}
    </FormControl>
  </Stack>;
};

export default FormField;
