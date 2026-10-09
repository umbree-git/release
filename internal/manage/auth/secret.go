package auth

type Sealer struct{}

func LoadSealer(path string) (*Sealer, error) { return &Sealer{}, nil }

func (s *Sealer) Seal(plaintext []byte) ([]byte, error) { return nil, errNotBuilt }

func (s *Sealer) Open(sealed []byte) ([]byte, error) { return nil, errNotBuilt }
