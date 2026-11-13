// Fork addition: a field for a secret the server keeps to itself (a password,
// a token). The stored value never comes to the browser: the form gets the
// mask "***", which means "one is stored", and sends the mask back to keep
// it. This field shows that state in words instead of dots that look like a
// password: typing replaces the stored secret, leaving it empty keeps it,
// and "Remove" is an explicit action.
import * as React from 'react';
import { Button, IconButton, InputAdornment, Stack, Tooltip, Typography } from '@mui/material';
import { EyeInvisibleOutlined, EyeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { FormField } from './formField';

export const SECRET_MASK = '***';

// value: what the form holds ("***": keep the stored one). stored: the
// server has one. onChange gets the new value for the form.
const SecretInput = ({ id, label, value, stored, onChange, helperText, error, disabled, autoComplete = 'new-password' }) => {
  const { t } = useTranslation();
  const [show, setShow] = React.useState(false);
  const [removing, setRemoving] = React.useState(false);
  const keeps = stored && value === SECRET_MASK;

  return <FormField
    id={id}
    label={label}
    type={show ? 'text' : 'password'}
    value={keeps ? '' : value}
    disabled={disabled}
    autoComplete={autoComplete}
    error={error}
    helperText={helperText}
    placeholder={keeps ? t('forms.secret.placeholder', 'Saved. Type here to replace it') : ''}
    onChange={(text) => {
      if (text !== '') {
        setRemoving(false);
        onChange(text);
      } else {
        // emptied again: back to the stored one, unless it is being removed
        onChange(stored && !removing ? SECRET_MASK : '');
      }
    }}
    endAdornment={
      <InputAdornment position="end">
        <Tooltip title={show ? t('tooltip.hidePassword', 'Hide password') : t('tooltip.showPassword', 'Show password')}>
          <IconButton
            aria-label={show ? t('tooltip.hidePassword', 'Hide password') : t('tooltip.showPassword', 'Show password')}
            onClick={() => setShow(!show)}
            onMouseDown={(event) => event.preventDefault()}
            edge="end"
            size="large"
          >
            {show ? <EyeOutlined /> : <EyeInvisibleOutlined />}
          </IconButton>
        </Tooltip>
      </InputAdornment>
    }
  >
    {keeps && !disabled && <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
      <Typography variant="caption" color="text.secondary">
        {t('forms.secret.kept', 'One is saved on the server and is not shown here. Leave the field empty to keep it.')}
      </Typography>
      <Button size="small" color="inherit" onClick={() => { setRemoving(true); onChange(''); }}>
        {t('forms.secret.remove', 'Remove it')}
      </Button>
    </Stack>}
    {stored && removing && value === '' && <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
      <Typography variant="caption" color="text.secondary">
        {t('forms.secret.removing', 'The saved one is removed when you save.')}
      </Typography>
      <Button size="small" color="inherit" onClick={() => { setRemoving(false); onChange(SECRET_MASK); }}>
        {t('forms.secret.keep', 'Keep it')}
      </Button>
    </Stack>}
  </FormField>;
};

export default SecretInput;
