// material-ui
import * as React from 'react';
import { Alert, Box, Button, Checkbox, CircularProgress, FormControl, FormHelperText, Grid, Icon, IconButton, InputLabel, List, ListItem, ListItemButton, MenuItem, NativeSelect, Select, Stack, TextField, Tooltip, Typography } from '@mui/material';
import Dialog from '@mui/material/Dialog';
import {TreeItem, TreeView} from '@mui/x-tree-view';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import DialogTitle from '@mui/material/DialogTitle';
import { LoadingButton } from '@mui/lab';
import { useFormik, FormikProvider } from 'formik';
import * as Yup from 'yup';
import { useTranslation } from 'react-i18next';
import { FolderOpenOutlined, ClockCircleOutlined, DashboardOutlined, DeleteOutlined, DownOutlined, LockOutlined, SafetyOutlined, UpOutlined, ExpandOutlined, FolderFilled, FileFilled, FileOutlined, LoadingOutlined, FolderAddFilled, FolderViewOutlined } from "@ant-design/icons";
import * as API from '../api';
import { current } from '@reduxjs/toolkit';
import { json } from 'react-router';
import { NewFolderButton } from './newFileModal';
import FilePickerShortcuts, { startFolders } from './filePickerShortcuts'; // Fork addition

function transformToTree(data) {
  const root = {
    id: Object.keys(data)[0],
    label: Object.keys(data)[0].split('/').pop(),
    opened: true,
    file: {
      isDir: true
    },
    children: []
  };

  if (root.label === '') {
    root.label = '/';
  }

  function buildTree(node) {
    const path = node.id;
    if (data[path]) {
      
      for (const item of data[path]) {
        const childPath = `${path}/${item.name}`.replace(/^\/\//, '/');
        const child = {
          id: childPath,
          label: item.name,
          file: item,
        };

        if (data[childPath]) {
          child.children = [];
          buildTree(child);
        }

        node.children.push(child);
      }
    }
  }

  buildTree(root);
  return root;
}

// Fork addition: _value, the path the field holds now; the picker opens at its folder
const FilePickerModal = ({ raw, open, cb, restic, OnClose, onPick, canCreate, _storage = '', _path = '', select='any', _value = '' }) => {
  const { t } = useTranslation();
  const [selectedStorage, setSelectedStorage] = React.useState(_storage);
  const [selectedPath, setSelectedPath] = React.useState(_path);
  const [files, setFiles] = React.useState({});
  const [storages, setStorages] = React.useState([]);
  const [directoriesAsTree, setDirectoriesAsTree] = React.useState({});
  const [selectedFullPath, setSelectedFullPath] = React.useState('');
  const [startFolder, setStartFolder] = React.useState(''); // Fork addition

  let explore = !onPick;

  // Fork addition: `reset` makes this folder the top of the tree, `quiet` keeps a failure silent;
  // a listing resolves to true
  const updateFiles = (storage, path, force, reset, quiet) => {
    let resetStorage = reset || storage != selectedStorage;

    // if already loaded, just update the tree
    if(!resetStorage && (!force && files[path])) {
      setSelectedStorage(storage);
      setSelectedPath(path);
      return;
    } else {
      return API.storage.listDir(storage, path, quiet).then(({data}) => {
        setFiles((current) => {
          if(resetStorage) {
            current = {
              [data.path]: data.directory || []
            };
          } else {
            current[data.path] = data.directory || [];
          }
          
          setDirectoriesAsTree(transformToTree(current));
          return current;
        });
        
        setStorages(data.storages);
        setSelectedStorage(data.storage);
        setSelectedPath(data.path);
        setPath(data.storage, data.path || '/', data.fullpath || '/');
        return true;
      });
    }
  }

  // Fork addition: shows a folder of the local disk as the top of the tree
  const goTo = (path, quiet) => updateFiles('local', encodeURIComponent(path), true, true, quiet).catch(() => false);

  function convertToTreeView(treeData) {
    let nodeCounter = 0;
  
    const renderTree = (node) => {
      nodeCounter++;
      return (
        <TreeItem key={node.id} nodeId={node.id} label={node.label} expanded={node.opened} icon={(node.file && node.file.isDir) ? (node.children == null ? <FolderFilled /> : null) : <FileOutlined />}>
          {Array.isArray(node.children)
            ? node.children.map((child) => renderTree(child))
            : (
              (node.file && node.file.isDir) ? 
              <TreeItem key={node.id + "load"} nodeId={(++nodeCounter).toString()} label={"Loading..."} icon={<LoadingOutlined />} />
              : null
            )}
        </TreeItem>
      );
    };
  
    return (
      <TreeView
        /* Fork addition: the top folder is shown open, also after jumping to another one */
        key={treeData.id}
        defaultExpanded={[treeData.id]}
        onNodeSelect={handleToggleNode}
        aria-label="file system navigator"
        defaultCollapseIcon={<FolderOpenOutlined />}
        defaultExpandIcon={<FolderFilled />}
        style={{
          background: 'rgba(0,0,0,0.2)',
          padding: '10px',
        }}
      >
        {renderTree(treeData)}
      </TreeView>
    );
  }

  const setPath = (selectedStorage, p, fullPath) => {
    if(raw) {
      let realSelected = restic ? ((selectedStorage == "local" || selectedStorage == "") ? selectedStorage : ("rclone" + ':' + selectedStorage)) : selectedStorage;
      let realNodePath = restic && selectedStorage != "local" ? p.replace(/^\//, '') : p;
      setSelectedFullPath(selectedStorage == "local" ? p : (realSelected + ':' + realNodePath));
    } else {
      setSelectedFullPath(fullPath);
    }
  }

  const handleToggleNode = (event, nodePath) => {
    // Fork addition: the top of the tree can be any local folder, not only "/"
    if (nodePath == "/" || (selectedStorage == "local" && nodePath === directoriesAsTree.id)) {
      updateFiles(selectedStorage, nodePath);
      setPath(selectedStorage, nodePath, nodePath);
      return;
    }

    // look for the nodePath in the list of files and check if it is a directory
    let nodeParent = nodePath.split('/').slice(0, -1).join('/');
    nodeParent =  nodeParent.replace(/\/$/, '');
    if(nodeParent == '') {
      nodeParent = '/';
    }
    let filename = nodePath.split('/').pop();

    if(files[nodeParent]) {
      let file = files[nodeParent].find((f) => f.name == filename);
      console.log("file", file);
      if(file.isDir) {
        updateFiles(selectedStorage, nodePath);
      }

      if(select == 'any' || (
        (select == 'file' && !file.isDir) ||
        (select == 'folder' && file.isDir)
      )) {
        setPath(selectedStorage, nodePath, file.fullPath);
      }
    }
  }

  React.useEffect(() => {
    // Fork addition: start where the field's current value is, when that can be listed
    (async () => {
      const folders = (!_storage || _storage == 'local') && !_path ? startFolders(_value) : [];
      for (const folder of folders) {
        if (await goTo(folder, true)) {
          setStartFolder(folder);
          return;
        }
      }
      updateFiles(_storage, _path);
    })();
  }, []);

  return (
    <>
      <Dialog open={open} onClose={() => OnClose()} fullWidth maxWidth="md">
          <DialogTitle>{explore ? t('mgmt.storage.seeFile') : t('mgmt.storage.pickFile')}</DialogTitle>
          <DialogContent>
            <DialogContentText>
              {(storages && files) ? <>
              {/* Fork addition */}
              {!explore && <FilePickerShortcuts
                current={selectedStorage == 'local' ? directoriesAsTree.id : ''}
                startFolder={startFolder}
                onGo={(path) => goTo(path)}
              />}
              <Stack direction="row" spacing={2}>
              {!explore && <div style={{background: 'rgba(0,0,0,0.1)'}}>
                <Stack spacing={2} style={{padding: '10px'}}>
                    <List style={{padding: '10px', width: '130px', overflow: 'auto'}}>
                      <ListItem key={'asdads'} disablePadding>
                        <Typography variant="h6" component={'span'}>Storage</Typography>
                      </ListItem>
                      {storages.map((storage) => (
                        <ListItem key={storage.name} disablePadding >
                           <ListItemButton
                            onClick={() => {updateFiles(storage.name, '', true, true /* Fork addition: back to the top of that storage */);}}
                            style={{background: selectedStorage === storage.name ? 'rgba(0,0,0,0.1)' : 'transparent' }}
                            >{storage.name}</ListItemButton>
                        </ListItem>
                      ))}
                    </List>
                    <Stack alignItems="center" spacing={2}>
                      <Button href="/cosmos-ui/storage" target="_blank" style={{width: '110px', textAlign:'center'}}>Add external storages</Button>
                    </Stack>
                    </Stack>
                </div>}
                <div style={{flexGrow: 1, minHeight: '300px'}}>
                  {!explore && <Stack style={{padding: '5px 5px 5px 10px', margin: '0px 0px 5px 0px', background: 'rgba(0,0,0,0.1)'}} direction="row" spacing={2} alignContent={'center'} alignItems={'center'}>
                    <div>{canCreate ?  <NewFolderButton cb={
                      (fullpath, filename) => {
                        updateFiles(selectedStorage, selectedPath, true);
                        setTimeout(() => {
                          setPath(selectedStorage, selectedPath + "/" + filename, fullpath);
                          setSelectedPath(selectedPath + "/" + filename);
                        }, 1000);
                      }
                    } storage={selectedStorage} path={selectedPath}/> : null}</div>
                    <div>{t('mgmt.storage.selected')}: {selectedFullPath}</div>
                  </Stack>}
                  {convertToTreeView(directoriesAsTree)}
                </div>
              </Stack>
              </> : <Box
              display="flex"
              alignItems="center" 
              justifyContent="center" 
              width="100%"  
              height="100%" 
              color="text.primary"  
              p={2}
            >
              <CircularProgress />
            </Box>}
            </DialogContentText>
          </DialogContent>
          {!explore && <DialogActions>
            <Button onClick={() => OnClose()}>{t('global.cancelAction')}</Button>
            <Button onClick={() => {onPick(selectedFullPath); OnClose();}} autoFocus>
              {t('global.confirmAction')}
            </Button>
          </DialogActions>}
          {explore && <DialogActions>
            <Button onClick={() => OnClose()}>{t('global.close')}</Button>
          </DialogActions>}
      </Dialog>
    </>
  );
};

// Fork addition: value, the path the field holds now
const FilePickerButton = ({ disabled, raw, canCreate, restic, onPick, size = '100%', storage = '', path = '', select="any", tooltip, value }) => {
  const [open, setOpen] = React.useState(false);
  const { t } = useTranslation();

  return (
    <>
      {/* Fork addition: tooltip; the span keeps it working while the button is disabled and takes the button's place in the layout */}
      <Tooltip title={tooltip || t('tooltip.browseFiles', 'Browse files')}><span style={{display: 'inline-flex'}}><IconButton onClick={() => setOpen(true)} disabled={disabled}>
        {onPick ? <><FolderAddFilled style={{fontSize: size}}/></> : <FolderViewOutlined style={{fontSize: size}}/>}
      </IconButton></span></Tooltip>
      {open && <FilePickerModal canCreate={canCreate} restic={restic} raw={raw} select={select} open={open} onPick={onPick} OnClose={() => setOpen(false)} _storage={storage} _path={path} _value={value} />}
    </>
  );
}

export default FilePickerModal;
export { FilePickerButton };