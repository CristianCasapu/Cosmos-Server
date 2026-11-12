// Fork addition: the "Timezone" setting. A free-text field for an IANA zone
// name, with the browser's list of zones as suggestions where the browser has
// one; a plain text field otherwise.
import * as React from 'react';
import {
  Autocomplete,
  FormHelperText,
  Grid,
  InputLabel,
  OutlinedInput,
  Stack,
  TextField,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { FormReadOnlyContext } from './users/formShortcuts';

// Intl.supportedValuesOf is missing in older browsers (Safari before 15.4, Firefox before 93).
const browserTimezones = () => {
  try {
    if (typeof Intl !== 'undefined' && typeof Intl.supportedValuesOf === 'function') {
      const zones = Intl.supportedValuesOf('timeZone');
      return Array.isArray(zones) && zones.length ? zones : null;
    }
  } catch (e) {
    // fall back to the plain field
  }
  return null;
};

const TimezoneInput = ({ name = 'Timezone', formik, detected, disabled }) => {
  const { t } = useTranslation();
  const readOnly = React.useContext(FormReadOnlyContext);
  const zones = React.useMemo(browserTimezones, []);
  const value = formik.values[name] || '';
  const error = formik.touched[name] && formik.errors[name];
  disabled = disabled || readOnly;

  return <Grid item xs={12}>
    <Stack spacing={1}>
      <InputLabel htmlFor={name}>{t('mgmt.config.timezone.label', 'Time zone for new apps')}</InputLabel>
      {zones ? <Autocomplete
        freeSolo
        fullWidth
        id={name}
        options={zones}
        disabled={disabled}
        inputValue={value}
        // "reset" is the component putting back its own (uncontrolled) selection: not something the admin typed
        onInputChange={(event, text, reason) => {
          if (reason !== 'reset') formik.setFieldValue(name, text || '');
        }}
        onChange={(event, option) => formik.setFieldValue(name, option || '')}
        renderInput={(params) => <TextField
          {...params}
          name={name}
          placeholder={detected || ''}
          error={Boolean(error)}
          onBlur={formik.handleBlur}
        />}
      /> : <OutlinedInput
        id={name}
        name={name}
        type="text"
        fullWidth
        disabled={disabled}
        value={value}
        placeholder={detected || ''}
        autoComplete="off"
        error={Boolean(error)}
        onBlur={formik.handleBlur}
        onChange={formik.handleChange}
      />}
      <FormHelperText>
        {t('mgmt.config.timezone.helper', 'Given to apps that ask for the server\'s time zone (TZ=auto) when they are installed. Leave empty to use the server\'s own time zone. Apps that are already installed keep theirs.')}
      </FormHelperText>
      {error && (
        <FormHelperText error id="standard-weight-helper-text-name-login">
          {error}
        </FormHelperText>
      )}
    </Stack>
  </Grid>;
};

export default TimezoneInput;
