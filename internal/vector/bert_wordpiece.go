package vector

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type bertTokenizer struct {
	vocab       map[string]int
	unkID       int
	clsID       int
	sepID       int
	padID       int
	maxSequence int
	doLowerCase bool
}

func loadBERTTokenizer(vocabPath string) (*bertTokenizer, error) {
	file, err := os.Open(vocabPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	vocab := map[string]int{}
	scanner := bufio.NewScanner(file)
	index := 0
	for scanner.Scan() {
		token := strings.TrimSpace(scanner.Text())
		if token == "" {
			continue
		}
		vocab[token] = index
		index++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	unkID, ok := vocab["[UNK]"]
	if !ok {
		return nil, fmt.Errorf("vocab missing [UNK]")
	}
	clsID, ok := vocab["[CLS]"]
	if !ok {
		return nil, fmt.Errorf("vocab missing [CLS]")
	}
	sepID, ok := vocab["[SEP]"]
	if !ok {
		return nil, fmt.Errorf("vocab missing [SEP]")
	}
	padID, ok := vocab["[PAD]"]
	if !ok {
		return nil, fmt.Errorf("vocab missing [PAD]")
	}

	return &bertTokenizer{vocab: vocab, unkID: unkID, clsID: clsID, sepID: sepID, padID: padID, maxSequence: 256, doLowerCase: true}, nil
}

func (t *bertTokenizer) Encode(text string) ([]int64, []int64, []int64) {
	tokens := t.wordPieceTokenize(t.basicTokenize(text))
	maxPieces := t.maxSequence - 2
	if len(tokens) > maxPieces {
		tokens = tokens[:maxPieces]
	}
	ids := make([]int64, 0, len(tokens)+2)
	mask := make([]int64, 0, len(tokens)+2)
	typeIDs := make([]int64, 0, len(tokens)+2)
	ids = append(ids, int64(t.clsID))
	mask = append(mask, 1)
	typeIDs = append(typeIDs, 0)
	for _, token := range tokens {
		id, ok := t.vocab[token]
		if !ok {
			id = t.unkID
		}
		ids = append(ids, int64(id))
		mask = append(mask, 1)
		typeIDs = append(typeIDs, 0)
	}
	ids = append(ids, int64(t.sepID))
	mask = append(mask, 1)
	typeIDs = append(typeIDs, 0)
	return ids, mask, typeIDs
}

func (t *bertTokenizer) basicTokenize(text string) []string {
	text = cleanText(text)
	if t.doLowerCase {
		text = normalizeCaseAndAccents(text)
	}
	tokens := make([]string, 0)
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			flush()
		case isPunctuation(r):
			flush()
			tokens = append(tokens, string(r))
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func (t *bertTokenizer) wordPieceTokenize(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) > 100 {
			out = append(out, "[UNK]")
			continue
		}
		start := 0
		pieces := make([]string, 0, 4)
		failed := false
		for start < len(token) {
			end := len(token)
			found := ""
			for start < end {
				substr := token[start:end]
				if start > 0 {
					substr = "##" + substr
				}
				if _, ok := t.vocab[substr]; ok {
					found = substr
					break
				}
				end--
			}
			if found == "" {
				failed = true
				break
			}
			pieces = append(pieces, found)
			if strings.HasPrefix(found, "##") {
				start += len(found) - 2
			} else {
				start += len(found)
			}
		}
		if failed {
			out = append(out, "[UNK]")
			continue
		}
		out = append(out, pieces...)
	}
	return out
}

func cleanText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == 0 || r == 0xfffd || (unicode.IsControl(r) && !unicode.IsSpace(r)) {
			continue
		}
		if unicode.IsSpace(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripAccents(text string) string {
	decomposed := norm.NFD.String(text)
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func normalizeCaseAndAccents(text string) string {
	needsUnicode := false
	needsLower := false
	for _, r := range text {
		if r > unicode.MaxASCII {
			needsUnicode = true
			break
		}
		if r >= 'A' && r <= 'Z' {
			needsLower = true
		}
	}
	if !needsUnicode {
		if !needsLower {
			return text
		}
		return strings.ToLower(text)
	}
	return stripAccents(strings.ToLower(text))
}

func isPunctuation(r rune) bool {
	if unicode.IsPunct(r) {
		return true
	}
	if unicode.IsSymbol(r) {
		return true
	}
	return false
}
