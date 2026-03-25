package runtime

import "errors"

func getOrCreateAssocWithReader[T any](
	store *udpAssocStore,
	key string,
	creator func() (T, error),
	invalidTypeErr string,
	ensureReader func(T),
) (T, error) {
	var zero T
	created, _, err := store.GetOrCreateWithStatus(key, func() (any, error) {
		return creator()
	})
	if err != nil {
		return zero, err
	}
	v, ok := created.(T)
	if !ok {
		return zero, errors.New(invalidTypeErr)
	}
	if ensureReader != nil {
		ensureReader(v)
	}
	return v, nil
}
