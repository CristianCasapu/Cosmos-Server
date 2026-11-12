import dnsList from './dns-list.json';
import dnsConfig from './dns-config.json';
import DOMPurify from 'dompurify';

  import * as React from 'react';
  import {
    Checkbox,
    Divider,
    FormControlLabel,
    Grid,
    InputLabel,
    OutlinedInput,
    Stack,
    Typography,
    FormHelperText,
    TextField,
    MenuItem,
    AccordionSummary,
    AccordionDetails,
    Accordion,
    Chip,
    Box,
    FormControl,
    IconButton,
    InputAdornment,
    Alert,
  
  } from '@mui/material';
  import { Field } from 'formik';
  import { DownOutlined, UpOutlined } from '@ant-design/icons';

  import { EyeOutlined, EyeInvisibleOutlined } from '@ant-design/icons';
import { useState } from 'react';
import { CosmosCollapse, CosmosSelect,CosmosCheckbox, CosmosInputText } from '../pages/config/users/formShortcuts';
import { useTranslation } from 'react-i18next';

// DNS providers records can be managed through (DynDNS), mirrors dnsrecords.SupportedProviders()
export const recordProviders = ["cloudflare", "desec", "digitalocean", "duckdns", "gandiv5", "hetzner", "namecheap", "ovh", "porkbun", "route53"];

// the provider select: DynDNS-capable providers first, then the certificate-only ones
const providerOptions = (t) => {
  const withRecords = dnsList.filter((dns) => recordProviders.includes(dns)).sort();
  const certsOnly = dnsList.filter((dns) => !recordProviders.includes(dns)).sort();
  return [
    ["", "DISABLE"],
    ...withRecords.map((dns) => [dns, dns + " · " + t('mgmt.config.zones.providerWithDyndns')]),
    ["__certsonly", t('mgmt.config.zones.providerCertsOnly'), true],
    ...certsOnly.map((dns) => [dns, dns]),
  ];
};

// children render between the provider select and its credentials, so a form
// can show what the chosen provider enables before asking for its tokens
export const DnsChallengeComp = ({ name, configName, style, multiline, type, placeholder, onChange, label, formik, children }) => {
    const { t } = useTranslation();
    const filterVars = (obj) => {
      const newObj = {};
      Object.keys(obj).forEach((key) => {
        if (obj[key] !== '' && dnsConfig[formik.values[name]].vars.includes(key)) {
          newObj[key] = obj[key];
        }
      });
      return newObj;
    };

    return <><CosmosSelect
      name={name}
      label={label}
      formik={formik}
      onChange={(e) => {
        onChange && onChange(e);
      }}
      options={providerOptions(t)}
    />

      {children}

      <Grid item xs={12}>
        <Stack spacing={2}>
        {formik.values[name] && dnsConfig[formik.values[name]] &&<>
          {dnsConfig[formik.values[name]].vars.length > 0 && <CosmosCollapse title={t('mgmt.config.zones.providerSetup', { provider: formik.values[name] })} >
          <Stack spacing={2}>
          <Alert severity="info">
            Please be careful you are filling the correct values. Check the doc if unsure. Leave blank unused variables. <br />
            Doc link: <a href={dnsConfig[formik.values[name]].url} rel="noopener noreferrer" target="_blank">{dnsConfig[formik.values[name]].url}</a>
          </Alert>
          <div className="raw-table">
            <div dangerouslySetInnerHTML={{__html: DOMPurify.sanitize(dnsConfig[formik.values[name]].docs)}}></div>
          </div>
          {dnsConfig[formik.values[name]].vars.map((dnsVar) => <div>
            {dnsVar}:
              <OutlinedInput
                type={type ? type : 'text'}
                value={formik.values[configName] ? (formik.values[configName][dnsVar] || '') : ''}
                onChange={(...ar) => {
                  const newConfig = {
                    ...formik.values[configName],
                    [dnsVar]: ar[0].target.value
                  };
                  formik.setFieldValue(configName, filterVars(newConfig));
                }}
                placeholder={"leave blank if unused"}
                fullWidth
              />
          </div>)}
          </Stack>
          </CosmosCollapse>}
        </>}
        </Stack>
      </Grid>
    </>;
  }