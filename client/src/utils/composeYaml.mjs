// Fork addition: reads the text of a compose file (servapps/containers/docker-compose.jsx).
// js-yaml 5 loads YAML 1.2's core schema without merge keys; compose files use
// them (`<<: *defaults`), as js-yaml 4 read them, so the merge tag is added back.
import { load, CORE_SCHEMA, mergeTag } from 'js-yaml';

const schema = CORE_SCHEMA.withTags(mergeTag);

export const loadCompose = (text) => load(text, { schema });
