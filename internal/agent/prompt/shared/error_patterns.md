# Common Error Patterns in Spreadsheet Code Generation

## E1: Off-by-One Row Index
- Symptom: All values shifted up or down by 1 row
- Cause: Confusion between 0-based and 1-based indexing, or header row counted as data
- Fix: Verify header_row from Overview, set data_start = header_row + 1

## E2: Header Name Mismatch
- Symptom: KeyError or ValueError when looking up column by name
- Cause: Leading/trailing spaces in header, different case, or special characters
- Fix: Use `.strip().lower()` for header matching, log actual headers for debugging

## E3: Merged Cell Write Error
- Symptom: openpyxl.utils.exceptions.MergedCellError
- Cause: Attempting to write to a non-top-left cell in a merged range
- Fix: Check ws.merged_cells.ranges before writing, use safe_write() helper

## E4: Empty Cell Handling
- Symptom: TypeError: unsupported operand type(s) for +: 'NoneType' and 'float'
- Cause: Empty cells return None, not 0 or ""
- Fix: Always guard with `if value is not None:` or use `value or 0` for numbers

## E5: Date Format Inconsistency
- Symptom: Value mismatch on date cells
- Cause: openpyxl returns datetime objects, comparison expects string format
- Fix: Normalize dates with `.strftime('%Y-%m-%d')` or match expected format from sample data

## E6: Float Precision Loss
- Symptom: Expected "123.45" but got "123.44999999999999"
- Cause: IEEE 754 floating point arithmetic
- Fix: Use `round(value, N)` where N matches the expected decimal places

## E7: Wrong Sheet Targeted
- Symptom: Changes appear on wrong sheet, answer_position sheet is unmodified
- Cause: answer_position may reference a specific sheet (e.g., "Sheet2!B3")
- Fix: Parse sheet name from answer_position, or check Overview for which sheet contains the target