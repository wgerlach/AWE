package cwl

type Directory struct {
	CWLType_Impl `yaml:",inline" json:",inline" bson:",inline" mapstructure:",squash"`
	Location     string         `yaml:"location,omitempty" json:"location,omitempty" bson:"location,omitempty"`
	Path         string         `yaml:"path,omitempty" json:"path,omitempty" bson:"path,omitempty"`
	Basename     string         `yaml:"basename,omitempty" json:"basename,omitempty" bson:"basename,omitempty"`
	Listing      []CWL_location `yaml:"listing,omitempty" json:"listing,omitempty" bson:"listing,omitempty"`
}

func (d Directory) GetClass() CWLType_Type { return CWL_Directory }
func (d Directory) String() string         { return d.Path }
func (d Directory) GetLocation() string    { return d.Location } // for CWL_location
