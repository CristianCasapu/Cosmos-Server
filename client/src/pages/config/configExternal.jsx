import * as React from 'react';
import * as Yup from 'yup';
import MainCard from '../../components/MainCard';
import {
  Grid,
  Stack,
  FormHelperText,
} from '@mui/material';
import { PostgresProposals, PostgresTestButton, PostgresFoundNote, usePostgresProposals } from './forkPostgresProposals'; // Fork addition (plan 47)
import { postgresHostProblem, postgresNameProblem, postgresPasswordProblem } from '../../utils/postgresProposal'; // Fork addition (plan 47)
import { CosmosCheckbox, CosmosCollapse, CosmosInputPassword, CosmosInputText } from './users/formShortcuts';
import { useTranslation } from 'react-i18next';
import { FilePickerButton } from '../../components/filePicker';

// Fork addition (plan 47): the shapes the server checks again on save (utils/fork_postgres.go)
export const postgresValidation = (t) => ({
  PostgresHost: Yup.string().nullable().test('postgres-host', t('mgmt.config.general.monitoringDb.fork.hostValidation', 'A name or an address, optionally followed by :port, for example 127.0.0.1:5432 (no http://, no path)'),
    (value) => !postgresHostProblem(value)),
  PostgresDatabase: Yup.string().nullable().test('postgres-name', t('mgmt.config.general.monitoringDb.fork.nameValidation', 'At most 63 letters, digits and _ . $ -'),
    (value) => !postgresNameProblem(value)),
  PostgresUsername: Yup.string().nullable().test('postgres-name', t('mgmt.config.general.monitoringDb.fork.nameValidation', 'At most 63 letters, digits and _ . $ -'),
    (value) => !postgresNameProblem(value)),
  PostgresPassword: Yup.string().nullable().test('postgres-password', t('mgmt.config.general.monitoringDb.fork.passwordValidation', 'At most 1024 characters'),
    (value) => !postgresPasswordProblem(value)),
});

const ConfigExternal = ({ formik }) => {
  const { t } = useTranslation();
  const proposals = usePostgresProposals(); // Fork addition (plan 47)

  return (
    <Stack spacing={3}>
      <MainCard title="Docker">
        <Stack spacing={2}>
          <CosmosCheckbox
            label={t('mgmt.config.docker.skipPruneNetworkCheckbox.skipPruneNetworkLabel')}
            name="SkipPruneNetwork"
            formik={formik}
          />

          <CosmosCheckbox
            label={t('mgmt.config.docker.skipPruneImageCheckbox.skipPruneImageLabel')}
            name="SkipPruneImages"
            formik={formik}
          />

          <Stack direction={"row"} spacing={2} sx={{ alignItems: 'flex-end' }}>
            <FilePickerButton onPick={(path) => {
              if(path)
                formik.setFieldValue('DefaultDataPath', path);
            }} size="150%" select="folder" value={formik.values.DefaultDataPath /* Fork addition: open there */} />
            <CosmosInputText
              label={t('mgmt.config.docker.defaultDatapathInput.defaultDatapathLabel')}
              name="DefaultDataPath"
              formik={formik}
              placeholder={'/cosmos-storage'}
            />
          </Stack>
        </Stack>
      </MainCard>

      <MainCard title={t('mgmt.config.general.monitoringDbTitle')}>
        <Grid container spacing={3}>
          <Grid size={{ xs: 12 }}>
            <Stack spacing={1}>
              <FormHelperText>{t('mgmt.config.general.monitoringDb.introHelperText')}</FormHelperText>
              <PostgresFoundNote proposals={proposals} />{/* Fork addition (plan 47) */}

              <CosmosCollapse title={t('mgmt.config.general.monitoringDb.postgresTitle')}>
                <Grid container spacing={3}>
                  {/* Fork addition (plan 47): the PostgreSQL containers of this server, proposed with their source */}
                  <PostgresProposals formik={formik} proposals={proposals} />
                  <Grid size={{ xs: 12 }}>
                    <CosmosInputText
                      label={t('mgmt.config.general.monitoringDb.hostInput.hostLabel')}
                      name="PostgresHost"
                      formik={formik}
                      placeholder={'localhost:5432'}
                    />
                    <FormHelperText>{t('mgmt.config.general.monitoringDb.hostInput.hostHelperText')}</FormHelperText>
                  </Grid>

                  <Grid size={{ xs: 12 }}>
                    <CosmosInputText
                      label={t('mgmt.config.general.monitoringDb.databaseInput.databaseLabel')}
                      name="PostgresDatabase"
                      formik={formik}
                      placeholder={'cosmos'}
                    />

                    <CosmosInputText
                      label={t('mgmt.config.general.monitoringDb.usernameInput.usernameLabel')}
                      name="PostgresUsername"
                      formik={formik}
                    />

                    <CosmosInputPassword
                      label={t('mgmt.config.general.monitoringDb.passwordInput.passwordLabel')}
                      name="PostgresPassword"
                      autoComplete='new-password'
                      formik={formik}
                      noStrength
                    />
                  </Grid>

                  <PostgresTestButton formik={formik} />{/* Fork addition (plan 47) */}

                  <Grid size={{ xs: 12 }}>
                    <CosmosInputText
                      label={t('mgmt.config.general.monitoringDb.nodeNameInput.nodeNameLabel')}
                      name="MetricsNodeName"
                      formik={formik}
                    />
                    <FormHelperText>{t('mgmt.config.general.monitoringDb.nodeNameInput.nodeNameHelperText')}</FormHelperText>
                  </Grid>
                </Grid>
              </CosmosCollapse>
            </Stack>
          </Grid>
        </Grid>
      </MainCard>
    </Stack>
  );
};

export default ConfigExternal;
