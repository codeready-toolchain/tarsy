import { useState, useEffect, useRef, useCallback, memo } from 'react';
import {
  Paper,
  TextField,
  InputAdornment,
  IconButton,
  Typography,
} from '@mui/material';
import {
  Search,
  Clear,
  KeyboardArrowUp,
  KeyboardArrowDown,
} from '@mui/icons-material';

interface SessionSearchBarProps {
  matchCount: number;
  currentMatchIndex: number;
  onSearchChange: (debouncedTerm: string) => void;
  onNextMatch: () => void;
  onPrevMatch: () => void;
}

const DEBOUNCE_MS = 500;
const MIN_SEARCH_LENGTH = 3;

export const SessionSearchBar = memo(function SessionSearchBar({
  matchCount,
  currentMatchIndex,
  onSearchChange,
  onNextMatch,
  onPrevMatch,
}: SessionSearchBarProps) {
  const [inputValue, setInputValue] = useState('');
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const activeSearchTerm = useRef('');

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);

    const trimmed = inputValue.trim();
    if (!trimmed || trimmed.length < MIN_SEARCH_LENGTH) {
      if (activeSearchTerm.current) {
        activeSearchTerm.current = '';
        onSearchChange('');
      }
      return;
    }

    debounceRef.current = setTimeout(() => {
      activeSearchTerm.current = inputValue;
      onSearchChange(inputValue);
      debounceRef.current = null;
    }, DEBOUNCE_MS);

    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [inputValue, onSearchChange]);

  const handleChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setInputValue(e.target.value);
  }, []);

  const handleClear = useCallback(() => {
    setInputValue('');
    activeSearchTerm.current = '';
    onSearchChange('');
  }, [onSearchChange]);

  const hasActiveSearch = activeSearchTerm.current !== '';

  return (
    <Paper
      variant="outlined"
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1,
        px: 2,
        py: 1,
      }}
    >
      <TextField
        fullWidth
        placeholder="Search in session content (min 3 chars)..."
        variant="standard"
        size="small"
        value={inputValue}
        onChange={handleChange}
        slotProps={{
          input: {
            startAdornment: (
              <InputAdornment position="start">
                <Search fontSize="small" color="action" />
              </InputAdornment>
            ),
            disableUnderline: true,
          },
        }}
        sx={{ flex: 1 }}
      />
      {hasActiveSearch && (
        <Typography variant="caption" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>
          {matchCount === 0
            ? 'No matches'
            : `${currentMatchIndex + 1} of ${matchCount}`}
        </Typography>
      )}
      {hasActiveSearch && matchCount > 1 && (
        <>
          <IconButton size="small" onClick={onPrevMatch} aria-label="Previous match">
            <KeyboardArrowUp fontSize="small" />
          </IconButton>
          <IconButton size="small" onClick={onNextMatch} aria-label="Next match">
            <KeyboardArrowDown fontSize="small" />
          </IconButton>
        </>
      )}
      {inputValue && (
        <IconButton size="small" onClick={handleClear} aria-label="Clear search">
          <Clear fontSize="small" />
        </IconButton>
      )}
    </Paper>
  );
});
