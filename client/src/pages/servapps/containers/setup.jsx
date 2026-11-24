import React, { useCallback, useMemo, useState } from "react";
import { Field, Formik } from "formik";
import {
  Button,
  Stack,
  Grid,
  MenuItem,
  TextField,
  IconButton,
  Tooltip, // Fork addition: Tooltip, for the icon-only buttons
  FormHelperText,
  useMediaQuery,
  useTheme,
  Alert,
  FormControlLabel,
  Checkbox,
} from "@mui/material";
import MainCard from "../../../components/MainCard";
import {
  CosmosCheckbox,
  CosmosFormDivider,
  CosmosInputText,
  CosmosSelect,
} from "../../config/users/formShortcuts";
import { DeleteOutlined, PlusCircleOutlined } from "@ant-design/icons";
import * as API from "../../../api";
import { Button as LoadingButton } from "@mui/material"; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import LogsInModal from "../../../components/logsInModal";
import ResponsiveButton from "../../../components/responseiveButton";
import { useTranslation } from 'react-i18next';
import PermissionGuard from '../../../components/permissionGuard';
import { fieldProblems, limitsForForm } from '../../../utils/newContainer'; // Fork addition
import { PERM_RESOURCES } from '../../../utils/permissions';
import { useEditHints, ForkRestartChoice, ForkUserChoice, ForkPuidFix, ForkDeviceChoices, ForkLimits, ForkEnvAdd, ForkEditNotes } from './forkEditTabs'; // Fork addition (plan 47, I2c): the edit tab's proposals

const containerInfoFrom = (values) => {
  const labels = {};
  values.labels.forEach((label) => {
    labels[label.key] = label.value;
  });

  const devices = values.devices.map((device) => {
    return `${device.key}:${device.value}`;
  });

  const envVars = values.envVars.map((envVar) => {
    return `${envVar.key}=${envVar.value}`;
  });

  const realvalues = {
    ...values,
    envVars: envVars,
    labels: labels,
    devices: devices,
    cpus: parseFloat(values.cpus) || 0,
  };

  realvalues.interactive = realvalues.interactive ? 2 : 1;

  return realvalues;
};

const restartPolicies = [
  ["no", "No Restart"],
  ["always", "Always Restart"],
  ["on-failure", "Restart On Failure"],
  ["unless-stopped", "Restart Unless Stopped"],
];

const DockerContainerSetup = ({
  noCard,
  containerInfo,
  installer,
  OnChange,
  refresh,
  newContainer,
  OnForceSecure,
  afterImage, // Fork addition: shown under the image field (the image's defaults, in the new container form)
}) => {
  const { t } = useTranslation();
  const [pullRequest, setPullRequest] = useState(null);
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const padding = isMobile ? "6px 4px" : "12px 10px";
  const [latestImage, setLatestImage] = useState(containerInfo.Config.Image);
  const hints = useEditHints(containerInfo, !newContainer && !installer); // Fork addition (plan 47, I2c)

  const wrapCard = (children) => {
    if (noCard) return children;
    return <MainCard title={t('mgmt.servapps.newContainerTitle')}>{children}</MainCard>;
  };

  const initialValues = useMemo(() => {
    return {
      name: containerInfo.Name.replace("/", ""),
      image: containerInfo.Config.Image,
      restartPolicy: containerInfo.HostConfig.RestartPolicy.Name,
      user: containerInfo.Config.User,
      envVars: containerInfo.Config.Env.map((envVar) => {
        const [key, value] = envVar.split(/=(.*)/s);
        return { key, value };
      }),
      labels: Object.keys(containerInfo.Config.Labels).map((key) => {
        return { key, value: containerInfo.Config.Labels[key] };
      }),
      devices: containerInfo.HostConfig.Devices
        ? containerInfo.HostConfig.Devices.map((device) => {
            return typeof device == "string"
              ? {
                  key: device.split(":")[0],
                  value: device.split(":")[1] || device.split(":")[0],
                }
              : { key: device.PathOnHost, value: device.PathInContainer };
          })
        : [],
      interactive: containerInfo.Config.Tty && containerInfo.Config.OpenStdin,
      // Fork addition: the memory limit as a size ("512m"), not in bytes; the new form's own text kept (utils/newContainer.js)
      memLimit: limitsForForm(containerInfo).memLimit,
      cpus: limitsForForm(containerInfo).cpus,
    };
  }, [
    containerInfo.Config.Env,
    containerInfo.Config.Image,
    containerInfo.Config.Labels,
    containerInfo.Config.OpenStdin,
    containerInfo.Config.Tty,
    containerInfo.Config.User,
    containerInfo.HostConfig.Devices,
    containerInfo.HostConfig.RestartPolicy.Name,
    containerInfo.Name,
    containerInfo.HostConfig.Memory, // Fork addition
    containerInfo.HostConfig.NanoCpus, // Fork addition
  ]);

  const onValidate = useCallback(
    (values) => {
      const errors = {};
      if (!values.image) {
        errors.image = t('global.required');
      }
      if (!values.name && newContainer) {
        errors.name = t('global.required');
      }
      // env keys and labels key mustbe unique
      const envKeys = values.envVars.map((envVar) => envVar.key);
      const labelKeys = values.labels.map((label) => label.key);
      const uniqueEnvKeysKeys = [...new Set(envKeys)];
      const uniqueLabelKeys = [...new Set(labelKeys)];
      if (uniqueEnvKeysKeys.length !== envKeys.length) {
        errors.submit = t('mgmt.servapps.newContainer.env.keyNotUniqueError');
      }
      if (uniqueLabelKeys.length !== labelKeys.length) {
        errors.submit = t('mgmt.servapps.newContainer.label.labelNotUniqueError');
      }
      // Fork addition: a user or a limit the server would refuse is said under its field (utils/newContainer.js)
      const problems = fieldProblems({ Config: { User: values.user }, ForkLimits: { memLimit: values.memLimit, cpus: parseFloat(values.cpus) || 0 } });
      Object.keys(problems).forEach((field) => {
        if (!installer) errors[field] = t(problems[field][0], problems[field][1]);
      });
      // Fork addition (plan 47, I2c): a device added on the edit tab is a device of the server (checked again by the server)
      if (!newContainer) {
        const had = new Set((initialValues.devices || []).map((d) => d.key + '|' + d.value));
        const bad = values.devices.find((d) => !had.has(d.key + '|' + d.value)
          && (!/^\/dev\//.test(d.key || '') || /(^|\/)\.\.(\/|$)/.test(d.key || '') || !/^\//.test(d.value || '') || /(^|\/)\.\.(\/|$)/.test(d.value || '')));
        if (bad) errors.submit = t('mgmt.servapps.editTabs.device.problem', 'A device is a path under /dev on the server, given to an absolute path in the container, for example /dev/dri/renderD128.');
      }
      OnChange && OnChange(containerInfoFrom(values));
      return errors;
    },
    [OnChange, newContainer, installer, initialValues] // Fork addition: installer, initialValues
  );

  const onSubmit = useCallback(
    async (values, { setErrors, setStatus, setSubmitting }) => {
      if (values.image !== latestImage) {
        setPullRequest(
          () => (cb) => API.docker.pullImage(values.image, cb, true)
        );
        return;
      }

      if (newContainer) return false;
      delete values.name;

      setSubmitting(true);

      let realvalues = containerInfoFrom(values);

      return API.docker
        .updateContainer(containerInfo.Name.replace("/", ""), realvalues)
        .then((res) => {
          setStatus({ success: true, notes: res && res.notes }); // Fork addition (plan 47, I2c): what the edit changed by itself
          setSubmitting(false);
          refresh && refresh();
        })
        .catch((err) => {
          setStatus({ success: false });
          setErrors({ submit: err.message });
          setSubmitting(false);
        });
    },
    [containerInfo.Name, latestImage, newContainer, refresh]
  );

  return (
    <div
      style={{
        maxWidth: "1000px",
        width: "100%",
        margin: "auto",
        position: "relative",
      }}
    >
      <Formik
        initialValues={initialValues}
        enableReinitialize
        validate={onValidate}
        onSubmit={onSubmit}
      >
        {(formik) => (
          <form noValidate onSubmit={formik.handleSubmit}>
            {pullRequest && (
              <LogsInModal
                request={pullRequest}
                title={t('mgmt.servapps.newContainer.pullingImageStatus')}
                OnSuccess={() => {
                  setPullRequest(null);
                  setLatestImage(formik.values.image);
                }}
                OnClose={() => {
                  setPullRequest(null);
                }}
              />
            )}
            <Stack spacing={2}>
              {wrapCard(
                <>
                  <Grid container spacing={4}>
                    {!installer && (
                      <>
                        {newContainer && (
                          <CosmosInputText
                            name="name"
                            label={t('global.nameTitle')}
                            placeholder={t('global.nameTitle')}
                            formik={formik}
                            required /* Fork addition: mandatory fields are marked */
                          />
                        )}
                        <CosmosInputText
                          name="image"
                          label={t('mgmt.servApps.container.overview.imageTitle')}
                          placeholder={t('mgmt.servApps.container.overview.imageTitle')}
                          formik={formik}
                          required
                        />
                        {afterImage}
                        {/* Fork addition (plan 47, I2c): restart policy and user as choices on the edit tab */}
                        {!newContainer ? <ForkRestartChoice formik={formik} /> : <CosmosSelect
                          name="restartPolicy"
                          label={t('mgmt.servapps.newContainer.restartPolicyInput.restartPolicyLabel')}
                          placeholder={t('mgmt.servapps.newContainer.restartPolicyInput.restartPolicyPlaceholder')}
                          options={restartPolicies}
                          formik={formik}
                        />}
                        {!newContainer ? <><ForkUserChoice formik={formik} hints={hints} problem={formik.errors.user} /><ForkPuidFix formik={formik} hints={hints} /></> : <CosmosInputText
                          name="user"
                          label={t('global.user')}
                          placeholder={t('global.user')}
                          formik={formik}
                        />}
                        <CosmosCheckbox
                          name="interactive"
                          label={t('mgmt.servapps.newContainer.interactiveCheckbox.interactiveLabel')}
                          formik={formik}
                        />
                        {OnForceSecure && (
                          <Grid size={{ xs: 12 }}>
                            <Checkbox
                              type="checkbox"
                              as={FormControlLabel}
                              control={<Checkbox size="large" />}
                              label={t('mgmt.servapps.newContainer.forceSecureCheckbox.forceSecureLabel')}
                              checked={
                                containerInfo.Config.Labels.hasOwnProperty(
                                  "cosmos-force-network-secured"
                                ) &&
                                containerInfo.Config.Labels[
                                  "cosmos-force-network-secured"
                                ] === "true"
                              }
                              onChange={(e) => {
                                OnForceSecure(e.target.checked);
                              }}
                            />
                          </Grid>
                        )}
                      </>
                    )}

                    <CosmosFormDivider title={t('mgmt.servapps.newContainer.envTitle')} />
                    <Grid size={{ xs: 12 }}>
                      {formik.values.envVars.map((envVar, idx) => (
                        <Grid container key={idx}>
                          <Grid size={{ xs: 5 }} style={{ padding }}>
                            <TextField
                              name={`envVars.${idx}.key`}
                              onChange={formik.handleChange}
                              label={t('mgmt.servapps.newContainer.env.envKeyInput.envKeyLabel')}
                              fullWidth
                              value={envVar.key}
                            />
                          </Grid>
                          <Grid size={{ xs: 6 }} style={{ padding }}>
                            <TextField
                              name={`envVars.${idx}.value`}
                              onChange={formik.handleChange}
                              fullWidth
                              label={t('mgmt.servapps.newContainer.env.envValueInput.envValueLabel')}
                              value={envVar.value}
                            />
                          </Grid>
                          <Grid size={{ xs: 1 }} style={{ padding }}>
                            <Tooltip title={t('global.delete', 'Delete')}><IconButton
                              fullWidth
                              variant="outlined"
                              color="error"
                              onClick={() => {
                                const newEnvVars = [...formik.values.envVars];
                                newEnvVars.splice(idx, 1);
                                formik.setFieldValue("envVars", newEnvVars);
                              }}
                            >
                              <DeleteOutlined />
                            </IconButton></Tooltip>
                          </Grid>
                        </Grid>
                      ))}

                      {/* Fork addition (plan 47, I2c): the names the image uses are offered when adding one */}
                      {!newContainer && !installer ? <ForkEnvAdd formik={formik} hints={hints} image={containerInfo.Config.Image} /> : <ResponsiveButton
                        variant="outlined"
                        color="primary"
                        size="large"
                        onClick={() => {
                          const newEnvVars = [...formik.values.envVars];
                          newEnvVars.push({ key: "", value: "" });
                          formik.setFieldValue("envVars", newEnvVars);
                        }}
                        startIcon={<PlusCircleOutlined />}
                      >
                        {t('global.addAction')}
                      </ResponsiveButton>}
                    </Grid>

                    <CosmosFormDivider title={t('mgmt.servapps.newContainer.labelsTitle')} />
                    <Grid size={{ xs: 12 }}>
                      {formik.values.labels.map((label, idx) => (
                        <Grid container key={idx}>
                          <Grid size={{ xs: 5 }} style={{ padding }}>
                            <TextField
                              name={`labels.${idx}.key`}
                              fullWidth
                              label={t('mgmt.servapps.newContainer.env.envKeyInput.envKeyLabel')}
                              value={label.key}
                              onChange={formik.handleChange}
                            />
                          </Grid>
                          <Grid size={{ xs: 6 }} style={{ padding }}>
                            <TextField
                              name={`labels.${idx}.value`}
                              label={t('mgmt.servapps.newContainer.env.envValueInput.envValueLabel')}
                              fullWidth
                              value={label.value}
                              onChange={formik.handleChange}
                            />
                          </Grid>
                          <Grid size={{ xs: 1 }} style={{ padding }}>
                            <Tooltip title={t('global.delete', 'Delete')}><IconButton
                              fullWidth
                              variant="outlined"
                              color="error"
                              onClick={() => {
                                const newLabels = [...formik.values.labels];
                                newLabels.splice(idx, 1);
                                formik.setFieldValue("labels", newLabels);
                              }}
                            >
                              <DeleteOutlined />
                            </IconButton></Tooltip>
                          </Grid>
                        </Grid>
                      ))}
                      <ResponsiveButton
                        variant="outlined"
                        color="primary"
                        size="large"
                        onClick={() => {
                          const newLabels = [...formik.values.labels];
                          newLabels.push({ key: "", value: "" });
                          formik.setFieldValue("labels", newLabels);
                        }}
                        startIcon={<PlusCircleOutlined />}
                      >
                        {t('global.addAction')}
                      </ResponsiveButton>
                    </Grid>

                    <CosmosFormDivider title={t('mgmt.servapps.newContainer.devicesTitle')} />
                    <Grid size={{ xs: 12 }}>
                      {!newContainer && <ForkDeviceChoices formik={formik} hints={hints} />/* Fork addition (plan 47, I2c): the server's devices */}
                      {formik.values.devices.map((device, idx) => (
                        <Grid container key={idx}>
                          <Grid size={{ xs: 5 }} style={{ padding }}>
                            <TextField
                              name={`devices.${idx}.key`}
                              fullWidth
                              label={t('mgmt.servapps.newContainer.devices.hostPathInput.hostPathLabel')}
                              value={device.key}
                              onChange={formik.handleChange}
                            />
                          </Grid>
                          <Grid size={{ xs: 6 }} style={{ padding }}>
                            <TextField
                              name={`devices.${idx}.value`}
                              label={t('mgmt.servapps.newContainer.devices.containerPathInput.containerPathLabel')}
                              fullWidth
                              value={device.value}
                              onChange={formik.handleChange}
                            />
                          </Grid>
                          <Grid size={{ xs: 1 }} style={{ padding }}>
                            <Tooltip title={t('global.delete', 'Delete')}><IconButton
                              fullWidth
                              variant="outlined"
                              color="error"
                              onClick={() => {
                                const newDevices = [...formik.values.devices];
                                newDevices.splice(idx, 1);
                                formik.setFieldValue("devices", newDevices);
                              }}
                            >
                              <DeleteOutlined />
                            </IconButton></Tooltip>
                          </Grid>
                        </Grid>
                      ))}
                      <ResponsiveButton
                        variant="outlined"
                        color="primary"
                        size="large"
                        onClick={() => {
                          const newDevices = [...formik.values.devices];
                          newDevices.push({ key: "", value: "" });
                          formik.setFieldValue("devices", newDevices);
                        }}
                        startIcon={<PlusCircleOutlined />}
                      >
                        {t('global.addAction')}
                      </ResponsiveButton>
                    </Grid>

                    {!installer && (
                      <>
                        <CosmosFormDivider title={t('mgmt.servapps.newContainer.resourceLimitsTitle')} />
                        {/* Fork addition (plan 47, I2c): limits as choices under the server's totals */}
                        {!newContainer ? <ForkLimits formik={formik} hints={hints} errors={formik.errors} /> : <>
                        <CosmosInputText
                          name="memLimit"
                          label={t('mgmt.servapps.newContainer.memoryLimit')}
                          placeholder="e.g. 512m, 1g, or leave empty for unlimited"
                          formik={formik}
                        />
                        <CosmosInputText
                          name="cpus"
                          label={t('mgmt.servapps.newContainer.cpuLimit')}
                          placeholder="e.g. 0.5, 1, 2, or 0 for unlimited"
                          formik={formik}
                        />
                        </>}
                      </>
                    )}
                  </Grid>
                </>
              )}
              {!newContainer && (
                <MainCard>
                  <Stack direction="column" spacing={2}>
                    {formik.status && <ForkEditNotes notes={formik.status.notes} />/* Fork addition (plan 47, I2c) */}
                      {formik.errors.submit && (
                      <Grid size={{ xs: 12 }}>
                        <FormHelperText error>
                          {formik.errors.submit}
                        </FormHelperText>
                      </Grid>
                    )}
                    {formik.values.image !== latestImage && (
                      <Alert
                        severity="warning"
                        style={{ marginBottom: "15px" }}
                      >
                        {t('mgmt.servapps.newContainer.imageUpdateWarning')}
                      </Alert>
                    )}
                    <PermissionGuard permission={PERM_RESOURCES}>
                      <LoadingButton
                        fullWidth
                        disableElevation
                        disabled={formik.errors.submit}
                        loading={formik.isSubmitting}
                        size="large"
                        type="submit"
                        variant="contained"
                        color="primary"
                      >
                        {formik.values.image !== latestImage
                          ? t('mgmt.servapps.newContainer.pullImageButton')
                          : t('mgmt.servapps.newContainer.updateContainerButton')}
                      </LoadingButton>
                    </PermissionGuard>
                  </Stack>
                </MainCard>
              )}
            </Stack>
          </form>
        )}
      </Formik>
    </div>
  );
};

export default DockerContainerSetup;
