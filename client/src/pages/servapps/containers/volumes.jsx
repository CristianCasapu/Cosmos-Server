import React, { useCallback, useEffect, useMemo } from "react";
import { Formik } from "formik";
import {
  Button,
  Stack,
  Grid,
  MenuItem,
  TextField,
  IconButton,
  FormHelperText,
  CircularProgress,
  useTheme,
  Checkbox,
} from "@mui/material";
import MainCard from "../../../components/MainCard";
import { PlusCircleOutlined } from "@ant-design/icons";
import * as API from "../../../api";
import { Button as LoadingButton } from "@mui/material"; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import PrettyTableView from "../../../components/tableView/prettyTableView";
import ResponsiveButton from "../../../components/responseiveButton";
import { useTranslation } from 'react-i18next';
import { FilePickerButton } from "../../../components/filePicker";
import { Backups } from "../../backups/backups";
import BackupDialog from "../../backups/backupDialog";
import PermissionGuard from '../../../components/permissionGuard';
import { PERM_RESOURCES } from '../../../utils/permissions';
import { bindIsReadOnly, mountHostPath } from './forkContainerFixes'; // Fork addition: read-only mounts, real volume folders
import { PaidOnly } from '../../../components/paidVisibility'; // Fork addition
import { useEditHints, ForkDeclaredVolumes, ForkFolderNote, ForkSourcePlaces, ForkVolumeSource, ForkReadOnly } from './forkEditTabs'; // Fork addition (plan 47, I2c): the edit tab's proposals

const VolumeContainerSetup = ({
  noCard,
  containerInfo,
  frozenVolumes = [],
  refresh,
  newContainer,
  OnChange,
}) => {
  const { t } = useTranslation();
  const [volumes, setVolumes] = React.useState([]);
  const theme = useTheme();
  const hints = useEditHints(containerInfo, !newContainer); // Fork addition (plan 47, I2c)
  const appName = (containerInfo.Name || "").replace("/", ""); // Fork addition (plan 47, I2c): the new-container form has no name yet

  useEffect(() => {
    API.docker.volumeList().then((res) => {
      setVolumes(res.data.Volumes);
    });
  }, []);

  const refreshAll = () => {
    setVolumes(null);
    if (refresh)
      refresh().then(() => {
        API.docker.volumeList().then((res) => {
          setVolumes(res.data.Volumes);
        });
      });
    else
      API.docker.volumeList().then((res) => {
        setVolumes(res.data.Volumes);
      });
  };

  const formatSource = (mount) => {
    return mountHostPath(mount, volumes); // Fork addition: where Docker keeps the volume, not always /var/lib/docker
  }

  const wrapCard = (children) => {
    if (noCard) return children;
    return <MainCard title={t('mgmt.servapps.newContainer.volumesTitle')}>{children}</MainCard>;
  };

  const initialValues = useMemo(() => {
    return {
      volumes: [
        ...(containerInfo.HostConfig.Mounts || []),
        ...(containerInfo.HostConfig.Binds || []).map((bind) => {
          const [source, destination, mode] = bind.split(":");
          return {
            Type: source.startsWith("/") ? "bind" : "volume", // Fork addition (plan 47, I2c): "name:/path" is a named volume; saving it as a bind failed
            Source: source,
            Target: destination,
            ReadOnly: bindIsReadOnly(mode), // Fork addition
          };
        }),
      ],
    };
  }, [containerInfo.HostConfig.Binds, containerInfo.HostConfig.Mounts]);

  const onValidate = useCallback(
    (values) => {
      const errors = {};
      // check unique
      const volumes = values.volumes.map((volume) => {
        return `${volume.Target}`;
      });
      const unique = [...new Set(volumes)];
      if (unique.length !== volumes.length) {
        errors.submit = t('mgmt.servapps.newContainer.volumes.mountNotUniqueError');
      }
      // Fork addition (plan 47, I2c): what a new mount names is checked before saving (and again by the server)
      if (!newContainer) {
        const had = new Set((initialValues.volumes || []).map((v) => `${v.Type}|${v.Source}|${v.Target}`));
        const dots = /(^|\/)\.\.(\/|$)/;
        values.volumes.filter((v) => !had.has(`${v.Type}|${v.Source}|${v.Target}`)).forEach((v) => {
          if (!/^\//.test(v.Target || '') || dots.test(v.Target || '')) {
            errors.submit = t('mgmt.servapps.editTabs.mount.target', 'The folder in the container must be an absolute path such as /config, without "..".');
          } else if (v.Type === 'bind' && (!/^\//.test(v.Source || '') || dots.test(v.Source || ''))) {
            errors.submit = t('mgmt.servapps.editTabs.mount.source', 'The server folder must be an absolute path such as /mnt/data/app, without "..".');
          } else if (v.Type === 'volume' && v.Source && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(v.Source)) {
            errors.submit = t('mgmt.servapps.editTabs.mount.volume', 'A volume name may hold letters, digits and _ . - only, starting with a letter or a digit.');
          }
        });
      }
      OnChange && OnChange(values, volumes);
      return errors;
    },
    [OnChange, newContainer, initialValues] // Fork addition: newContainer, initialValues
  );

  const onSubmit = useCallback(
    async (values, { setErrors, setStatus, setSubmitting }) => {
      if (newContainer) return;
      setSubmitting(true);
      const realvalues = {
        Volumes: values.volumes.map((volume) => ({
          Type: volume.Type,
          Source: volume.Source,
          Target: volume.Target,
          ReadOnly: !!volume.ReadOnly, // Fork addition: was always false, so saving made every mount writable
        })),
      };
      return API.docker
        .updateContainer(containerInfo.Name.replace("/", ""), realvalues)
        .then((res) => {
          setStatus({ success: true });
          setSubmitting(false);
          refresh && refresh();
        })
        .catch((err) => {
          setStatus({ success: false });
          setErrors({ submit: err.message });
          setSubmitting(false);
          refresh && refresh();
        });
    },
    [containerInfo.Name, newContainer, refresh]
  );

  return (
    <Stack spacing={2} style={{ alignItems: "center" }}>
      <div
        style={{
          maxWidth: "1000px",
          width: "100%",
          margin: "",
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
              {wrapCard(
                <>
                  <Grid container spacing={4}>
                    {/* Fork addition (plan 47, I2c): what the image keeps data in and the container does not mount */}
                    {!newContainer && hints && <Grid size={{ xs: 12 }}>
                      <ForkDeclaredVolumes hints={hints} volumes={formik.values.volumes} app={appName}
                        onAdd={(row) => formik.setFieldValue("volumes", [...formik.values.volumes, row])} />
                    </Grid>}
                    <Grid size={{ xs: 12 }}>
                      {volumes && (
                        <PrettyTableView
            empty={{ title: t('emptyState.containerVolumes.title', 'No volumes or folders'), text: t('emptyState.containerVolumes.text', 'This app keeps nothing outside its container: its data is lost when it is re-created. Add a volume or a folder of the server with the + button above.') }} /* Fork addition: what this list is for and how it fills */
                          data={formik.values.volumes}
                          onRowClick={() => {}}
                          getKey={(r) => r.Id}
                          fullWidth
                          buttons={[
                            <PermissionGuard permission={PERM_RESOURCES}><ResponsiveButton
                              startIcon={<PlusCircleOutlined />}
                              variant="outlined"
                              color="primary"
                              onClick={() => {
                                formik.setFieldValue("volumes", [
                                  ...formik.values.volumes,
                                  !newContainer ? { Type: "bind", Source: "", Target: "", ReadOnly: false, forkNew: true } /* Fork addition (plan 47, I2c): a folder of a data place, chosen below */ : {
                                    Type: "volume",
                                    Name: "",
                                    Driver: "local",
                                    Source: "",
                                    Destination: "",
                                    RW: true,
                                  },
                                ]);
                              }}
                            >
                              {t('mgmt.servapps.newContainer.volumes.newMountButton')}
                            </ResponsiveButton></PermissionGuard>,
                          ]}
                          columns={[
                            {
                              title: "Type",
                              field: (r, k) => (
                                <div
                                  style={{
                                    fontWeight: "bold",
                                    wordSpace: "nowrap",
                                    overflow: "hidden",
                                    textOverflow: "ellipsis",
                                    maxWidth: "100px",
                                  }}
                                >
                                  <TextField
                                    className="px-2 my-2"
                                    disabled={frozenVolumes.includes(r.Source)}
                                    variant="outlined"
                                    id="Type"
                                    /* Fork addition: a name */
                                    select value={r.Type}
                                    name={`volumes[${k}].Type`}
                                    onChange={formik.handleChange}
                                    slotProps={{
                                      select: { SelectDisplayProps: { 'aria-label': t('mgmt.servapps.volumes.typeLabel', 'Type of mount') } }
                                    }}
                                  >
                                    <MenuItem value="bind">{t('mgmt.servapps.newContainer.volumes.bindInput')}</MenuItem>
                                    <MenuItem value="volume">{t('global.volume')}</MenuItem>
                                    <MenuItem value="tmpfs">tmpfs</MenuItem>
                                  </TextField>
                                </div>
                              ),
                            },
                            {
                              title: t('global.source'),
                              field: (r, k) => (
                                <div
                                  style={{
                                    fontWeight: "bold",
                                    wordSpace: "nowrap",
                                    overflow: "hidden",
                                    textOverflow: "ellipsis",
                                    maxWidth: "300px",
                                  }}
                                >
                                  {r.Type == "bind" ? (
                                    <Stack direction={"row"} spacing={2}>
                                    <FilePickerButton onPick={(path) => {
                                      if(path)
                                        formik.setFieldValue(`volumes[${k}].Source`, path);
                                    }} size="150%" select="folder" />
                                    <TextField
                                      className="px-2 my-2"
                                      variant="outlined"
                                      name={`volumes[${k}].Source`}
                                      id="Source"
                                      /* Fork addition: a name */
                                      disabled={frozenVolumes.includes(
                                        r.Source
                                      )} style={{ minWidth: "200px" }}
                                      value={r.Source}
                                      onChange={formik.handleChange}
                                      slotProps={{
                                        htmlInput: { 'aria-label': t('global.source') }
                                      }}
                                    />
                                    </Stack>
                                  ) : null}
                                  {/* Fork addition (plan 47, I2c): the folder's disk, owner and room; the data places for a new one */}
                                  {r.Type == "bind" && !newContainer && <div style={{ whiteSpace: "normal" }}>
                                    {r.forkNew && <ForkSourcePlaces row={r} hints={hints} app={appName} onPick={(path) => formik.setFieldValue(`volumes[${k}].Source`, path)} />}
                                    <ForkFolderNote row={r} hints={hints} />
                                  </div>}
                                  {r.Type == "bind" ? null : r.Type == "volume" && !newContainer ? (
                                    <ForkVolumeSource value={r.Source} volumes={volumes} disabled={frozenVolumes.includes(r.Source)}
                                      onChange={(name) => formik.setFieldValue(`volumes[${k}].Source`, name)} />
                                  ) : r.Type == "tmpfs" ? (
                                    <TextField
                                      className="px-2 my-2"
                                      variant="outlined"
                                      disabled
                                      /* Fork addition: a name */
                                      style={{ minWidth: "200px" }} value="(memory)"
                                      slotProps={{
                                        htmlInput: { 'aria-label': t('global.source') }
                                      }}
                                    />
                                  ) : (
                                    <TextField
                                      className="px-2 my-2"
                                      variant="outlined"
                                      name={`volumes[${k}].Source`}
                                      id="Source"
                                      disabled={frozenVolumes.includes(
                                        r.Source
                                      )}
                                      select
                                      /* Fork addition: a name */
                                      style={{ minWidth: "200px" }} value={r.Source}
                                      onChange={formik.handleChange}
                                      slotProps={{
                                        select: { SelectDisplayProps: { 'aria-label': t('global.source') } }
                                      }}
                                    >
                                      {[...volumes, r].map((volume) => (
                                        <MenuItem
                                          key={volume.Id || "last"}
                                          value={volume.Name || volume.Source}
                                        >
                                          {volume.Name || volume.Source}
                                        </MenuItem>
                                      ))}
                                    </TextField>
                                  )}
                                </div>
                              ),
                            },
                            {
                              title: t('global.target'),
                              field: (r, k) => (
                                <div
                                  style={{
                                    fontWeight: "bold",
                                    wordSpace: "nowrap",
                                    overflow: "hidden",
                                    textOverflow: "ellipsis",
                                    maxWidth: "300px",
                                  }}
                                >
                                  <TextField
                                    className="px-2 my-2"
                                    variant="outlined"
                                    name={`volumes[${k}].Target`}
                                    id="Target"
                                    /* Fork addition: a name */
                                    disabled={frozenVolumes.includes(r.Source)} style={{ minWidth: "200px" }}
                                    value={r.Target}
                                    onChange={formik.handleChange}
                                    slotProps={{
                                      htmlInput: { 'aria-label': t('global.target') }
                                    }}
                                  />
                                </div>
                              ),
                            },
                            // Fork addition (plan 47, I2c): read-only as a switch on the edit tab
                            ...(!newContainer ? [{
                              title: t('mgmt.servapps.editTabs.readOnly', 'Read-only'),
                              field: (r, k) => r.Type == "tmpfs" ? null : <ForkReadOnly checked={r.ReadOnly} disabled={frozenVolumes.includes(r.Source)}
                                onChange={(on) => formik.setFieldValue(`volumes[${k}].ReadOnly`, on)} />,
                            }] : []),
                            {
                              title: "",
                              field: (r) => {
                                return (
                                  <Stack direction="row" spacing={2}>
                                    <PermissionGuard permission={PERM_RESOURCES}><Button
                                    variant="outlined"
                                    color="primary"
                                    disabled={frozenVolumes.includes(r.Source)}
                                    onClick={() => {
                                      const newVolumes = [
                                        ...formik.values.volumes,
                                      ];
                                      newVolumes.splice(
                                        newVolumes.indexOf(r),
                                        1
                                      );
                                      formik.setFieldValue(
                                        "volumes",
                                        newVolumes
                                      );
                                    }}
                                  >
                                    {t('global.unmount')}
                                  </Button></PermissionGuard>
                                  {!newContainer && containerInfo.Name && (r.Target ? <PaidOnly>{/* Fork addition: needs a licence */}<BackupDialog preName={`${containerInfo.Name.replace("/", "").replace("/", "-")}-${r.Target.replace("/", "").replaceAll("/", "_")}`} preSource={formatSource(r.Source)} refresh={() => setTimeout(refreshAll, 1500)} /></PaidOnly> : null)}
                                  </Stack>
                                );
                              },
                            },
                          ]}
                        />
                      )}
                      {!volumes && (
                        <div style={{ height: "100px" }}>
                          <center>
                            <br />
                            <CircularProgress />
                          </center>
                        </div>
                      )}
                    </Grid>
                    <Grid size={{ xs: 12 }}>
                      <Stack direction="column" spacing={2}>
                        {formik.errors.submit && (
                          <Grid size={{ xs: 12 }}>
                            <FormHelperText error>
                              {formik.errors.submit}
                            </FormHelperText>
                          </Grid>
                        )}
                        {!newContainer && (
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
                              {t('mgmt.servapps.newContainer.volumes.updateVolumesButton')}
                            </LoadingButton>
                          </PermissionGuard>
                        )}
                      </Stack>
                    </Grid>
                  </Grid>
                </>
              )}
            </form>
          )}
        </Formik>
      </div>
      {!newContainer && <div
        style={{
          maxWidth: "1000px",
          width: "100%",
          margin: "",
          position: "relative",
      }}>
        {containerInfo && containerInfo.HostConfig && containerInfo.HostConfig.Mounts && <PaidOnly>{/* Fork addition: needs a licence */}<MainCard title={t('mgmt.backup.backups')}>
          <Backups pathFilters={
            containerInfo.HostConfig.Mounts.map((r) => formatSource(r.Source)).filter(Boolean)
          } />
        </MainCard></PaidOnly>}
      </div>}
    </Stack>
  );
};

export default VolumeContainerSetup;
