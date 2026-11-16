import Autocomplete from '@mui/material/Autocomplete';
import TextField from '@mui/material/TextField';
import {
  applyLabelSelection,
  isLabelName,
  LABEL_ALL,
  LABEL_ANY,
  LABEL_NONE,
} from '../../utils/notificationSettings.ts';

interface NotificationLabelFieldProps {
  value: string[];
  options: string[];
  onChange: (labels: string[]) => void;
}

function isPinned(token: string): boolean {
  return token === LABEL_ALL || token === LABEL_ANY || token === LABEL_NONE;
}

function optionLabel(token: string): string {
  if (token === LABEL_ALL) return 'All sessions';
  if (token === LABEL_ANY) return 'Any label';
  if (token === LABEL_NONE) return 'No label';
  return token;
}

export function NotificationLabelField({ value, options, onChange }: NotificationLabelFieldProps) {
  const choices = [LABEL_ALL, LABEL_ANY, LABEL_NONE, ...options];

  return (
    <Autocomplete
      multiple
      freeSolo
      autoHighlight
      id="notification-labels"
      options={choices}
      value={value}
      onChange={(_, next, reason) => {
        if (reason === 'clear') {
          onChange([LABEL_ALL]);
          return;
        }
        const tokens = next.map((item) => (typeof item === 'string' ? item : String(item)));
        onChange(applyLabelSelection(value, tokens));
      }}
      filterOptions={(opts, state) => {
        const query = state.inputValue.trim();
        const needle = query.toLowerCase();
        const pinned = opts.filter((opt) => isPinned(opt));
        const names = opts.filter((opt) => {
          if (isPinned(opt)) return false;
          return !needle || opt.toLowerCase().includes(needle);
        });
        if (!query) return [...pinned, ...names];
        const typed = isLabelName(query) && !names.includes(query) ? [query] : [];
        return [...typed, ...names, ...pinned];
      }}
      getOptionLabel={optionLabel}
      renderInput={(params) => (
        <TextField
          {...params}
          label="Labels"
          helperText="Names match if the session has any of them. All sessions ignores labels. An empty field matches nothing."
        />
      )}
    />
  );
}
